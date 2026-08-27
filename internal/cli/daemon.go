package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"facets.barnlab.dev/internal/project"
	"golang.org/x/sys/unix"
)

const (
	defaultTaskDaemonInterval          = 2 * time.Second
	defaultTaskDaemonRefreshTimeout    = 10 * time.Second
	defaultTaskDaemonHeartbeatInterval = 10 * time.Second
	minimumTaskDaemonInterval          = 250 * time.Millisecond
	maximumTaskDaemonInterval          = 5 * time.Minute
	minimumTaskDaemonRefreshTimeout    = 250 * time.Millisecond
	maximumTaskDaemonRefreshTimeout    = 5 * time.Minute
)

// taskDaemonSnapshot is the stable newline-delimited JSON contract consumed by
// the Facets Quickshell application. Every snapshot replaces the prior one.
type taskDaemonSnapshot struct {
	Type     string              `json:"type"`
	Projects []taskDaemonProject `json:"projects"`
}

type taskDaemonProject struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Directory string           `json:"directory"`
	Tasks     []taskDaemonTask `json:"tasks"`
}

type taskDaemonTask struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Priority  *int   `json:"priority"`
	Assignee  string `json:"assignee"`
	UpdatedAt string `json:"updated_at"`
	Top       bool   `json:"top"`
}

// taskDaemonError is recoverable. Consumers should retain their last valid
// snapshot while displaying Message and wait for the next snapshot.
type taskDaemonError struct {
	Type     string `json:"type"`
	Message  string `json:"message"`
	Retrying bool   `json:"retrying"`
}

type taskDaemonPollResult struct {
	snapshot taskDaemonSnapshot
	err      error
}

func (a *App) runTaskDaemon(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	fs := commandFlags(a.taskUsageCommand(cfg) + " daemon")
	interval := defaultTaskDaemonInterval
	if a.TaskDaemonInterval > 0 {
		interval = a.TaskDaemonInterval
	}
	refreshTimeout := defaultTaskDaemonRefreshTimeout
	if a.TaskDaemonRefreshTimeout > 0 {
		refreshTimeout = a.TaskDaemonRefreshTimeout
	}
	heartbeatInterval := defaultTaskDaemonHeartbeatInterval
	if a.TaskDaemonHeartbeatInterval > 0 {
		heartbeatInterval = a.TaskDaemonHeartbeatInterval
	}
	fs.DurationVar(&interval, "interval", interval, "delay between completed provider refreshes")
	fs.DurationVar(&refreshTimeout, "refresh-timeout", refreshTimeout, "maximum duration of one provider refresh")
	if code := a.parseFlags(stdout, cfg.format, fs, args, taskDaemonHelp()); code >= 0 {
		return code
	}
	if interval < minimumTaskDaemonInterval || interval > maximumTaskDaemonInterval {
		a.usageError(stdout, cfg.format, "--interval must be between 250ms and 5m", fmt.Sprintf("Use `%s daemon --interval 2s`", a.taskUsageCommand(cfg)))
		return 2
	}
	if refreshTimeout < minimumTaskDaemonRefreshTimeout || refreshTimeout > maximumTaskDaemonRefreshTimeout {
		a.usageError(stdout, cfg.format, "--refresh-timeout must be between 250ms and 5m", fmt.Sprintf("Use `%s daemon --refresh-timeout 10s`", a.taskUsageCommand(cfg)))
		return 2
	}
	if a.ProjectStore == nil {
		fmt.Fprintln(stderr, "facets tasks daemon: project registry is unavailable")
		return 1
	}

	provider, code := a.selectProvider(stdout, cfg)
	if code != 0 {
		return code
	}
	lock, err := a.acquireTaskDaemonLock()
	if err != nil {
		fmt.Fprintf(stderr, "facets tasks daemon: %v\n", err)
		return 1
	}
	defer lock.Close()

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	var lastEvent []byte
	eventFormat := cfg.format
	if !cfg.formatSet && eventFormat == "toon" {
		eventFormat = "json"
	}
	for {
		refreshCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
		refreshDone := make(chan taskDaemonPollResult, 1)
		go func() {
			snapshot, err := a.taskDaemonPoll(refreshCtx, provider)
			refreshDone <- taskDaemonPollResult{snapshot: snapshot, err: err}
		}()

		var result taskDaemonPollResult
	refresh:
		for {
			select {
			case <-ctx.Done():
				cancel()
				return 0
			case result = <-refreshDone:
				cancel()
				break refresh
			case <-heartbeat.C:
				if len(lastEvent) == 0 {
					continue
				}
				if _, writeErr := stdout.Write(lastEvent); writeErr != nil {
					cancel()
					fmt.Fprintf(stderr, "facets tasks daemon: write heartbeat: %v\n", writeErr)
					return 1
				}
			}
		}
		snapshot, refreshErr := result.snapshot, result.err

		var event any = snapshot
		if refreshErr != nil {
			if errors.Is(refreshErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				return 0
			}
			fmt.Fprintf(stderr, "facets tasks daemon: refresh failed: %v\n", refreshErr)
			event = taskDaemonError{Type: "error", Message: refreshErr.Error(), Retrying: true}
		}

		encoded, marshalErr := encodeTaskDaemonEvent(event, eventFormat)
		if marshalErr != nil {
			fmt.Fprintf(stderr, "facets tasks daemon: encode event: %v\n", marshalErr)
			return 1
		}
		if !bytes.Equal(encoded, lastEvent) {
			if _, writeErr := stdout.Write(encoded); writeErr != nil {
				fmt.Fprintf(stderr, "facets tasks daemon: write event: %v\n", writeErr)
				return 1
			}
			lastEvent = append(lastEvent[:0], encoded...)
		}

		timer := time.NewTimer(interval)
	wait:
		for {
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return 0
			case <-timer.C:
				break wait
			case <-heartbeat.C:
				if _, writeErr := stdout.Write(lastEvent); writeErr != nil {
					if !timer.Stop() {
						<-timer.C
					}
					fmt.Fprintf(stderr, "facets tasks daemon: write heartbeat: %v\n", writeErr)
					return 1
				}
			}
		}
	}
}

func (a *App) taskDaemonLockFile() string {
	if a.TaskDaemonLockPath != "" {
		return a.TaskDaemonLockPath
	}
	return fmt.Sprintf("/run/user/%d/facets-tasks-daemon.lock", os.Getuid())
}

func (a *App) acquireTaskDaemonLock() (*os.File, error) {
	lockPath := a.taskDaemonLockFile()
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open singleton lock: %w", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errors.New("another tasks daemon is already running")
		}
		return nil, fmt.Errorf("acquire singleton lock: %w", err)
	}
	return lock, nil
}

func encodeTaskDaemonEvent(event any, format string) ([]byte, error) {
	if format == "json" {
		encoded, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		return append(encoded, '\n'), nil
	}
	var encoded bytes.Buffer
	if err := writeDocument(&encoded, format, taskDaemonDocument(event)); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func taskDaemonDocument(event any) object {
	switch event := event.(type) {
	case taskDaemonSnapshot:
		projects := make(objectArray, len(event.Projects))
		for i, project := range event.Projects {
			tasks := make(objectArray, len(project.Tasks))
			for j, task := range project.Tasks {
				tasks[j] = object{
					{name: "id", value: task.ID},
					{name: "title", value: task.Title},
					{name: "status", value: task.Status},
					{name: "priority", value: priorityValue(task.Priority)},
					{name: "assignee", value: task.Assignee},
					{name: "updated_at", value: task.UpdatedAt},
					{name: "top", value: task.Top},
				}
			}
			projects[i] = object{
				{name: "id", value: project.ID},
				{name: "name", value: project.Name},
				{name: "directory", value: project.Directory},
				{name: "tasks", value: tasks},
			}
		}
		return object{{name: "type", value: event.Type}, {name: "projects", value: projects}}
	case taskDaemonError:
		return object{{name: "type", value: event.Type}, {name: "message", value: event.Message}, {name: "retrying", value: event.Retrying}}
	default:
		return object{{name: "event", value: fmt.Sprint(event)}}
	}
}

func (a *App) taskDaemonPoll(ctx context.Context, provider project.Provider) (taskDaemonSnapshot, error) {
	projects, err := provider.ListProjects(ctx)
	if err != nil {
		return taskDaemonSnapshot{}, fmt.Errorf("list projects: %w", err)
	}
	sort.SliceStable(projects, func(i, j int) bool {
		left, right := strings.ToLower(projects[i].Name), strings.ToLower(projects[j].Name)
		if left == right {
			return projects[i].ID < projects[j].ID
		}
		return left < right
	})
	if err := a.ProjectStore.SyncProjects(ctx, provider.Name(), projects); err != nil {
		return taskDaemonSnapshot{}, fmt.Errorf("sync project registry: %w", err)
	}

	type activeProject struct {
		item      project.Project
		directory string
	}
	activeProjects := make([]activeProject, 0, len(projects))
	for _, item := range projects {
		registered, err := a.ProjectStore.RegisteredProject(ctx, provider.Name(), item.ID)
		if err != nil {
			return taskDaemonSnapshot{}, fmt.Errorf("read project %q metadata: %w", item.ID, err)
		}
		if registered.DisabledAt != nil {
			continue
		}
		directory, _ := registered.Metadata["directory"].(string)
		activeProjects = append(activeProjects, activeProject{item: item, directory: directory})
	}

	snapshot := taskDaemonSnapshot{Type: "snapshot", Projects: make([]taskDaemonProject, 0, len(activeProjects))}
	open := project.StatusOpen
	for _, active := range activeProjects {
		item := active.item
		tasks, err := provider.ListTasks(ctx, item.ID, project.TaskFilter{Status: &open})
		if err != nil {
			return taskDaemonSnapshot{}, fmt.Errorf("list tasks for project %q: %w", item.ID, err)
		}
		sort.SliceStable(tasks, func(i, j int) bool {
			left, right := strings.ToLower(tasks[i].Title), strings.ToLower(tasks[j].Title)
			if left == right {
				return tasks[i].ID < tasks[j].ID
			}
			return left < right
		})

		daemonProject := taskDaemonProject{
			ID:        item.ID,
			Name:      item.Name,
			Directory: active.directory,
			Tasks:     make([]taskDaemonTask, 0, len(tasks)),
		}
		for _, task := range tasks {
			updatedAt := ""
			if !task.UpdatedAt.IsZero() {
				updatedAt = task.UpdatedAt.UTC().Format(time.RFC3339Nano)
			}
			daemonProject.Tasks = append(daemonProject.Tasks, taskDaemonTask{
				ID:        task.ID,
				Title:     task.Title,
				Status:    string(task.Status),
				Priority:  task.Priority,
				Assignee:  task.Assignee,
				UpdatedAt: updatedAt,
				Top:       isTopTask(task),
			})
		}
		snapshot.Projects = append(snapshot.Projects, daemonProject)
	}
	return snapshot, nil
}
