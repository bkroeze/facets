package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"facets.barnlab.dev/internal/project"
)

const (
	defaultTaskDaemonInterval = 2 * time.Second
	minimumTaskDaemonInterval = 250 * time.Millisecond
	maximumTaskDaemonInterval = 5 * time.Minute
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
}

// taskDaemonError is recoverable. Consumers should retain their last valid
// snapshot while displaying Message and wait for the next snapshot.
type taskDaemonError struct {
	Type     string `json:"type"`
	Message  string `json:"message"`
	Retrying bool   `json:"retrying"`
}

func (a *App) runTaskDaemon(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	fs := commandFlags(a.taskUsageCommand(cfg) + " daemon")
	interval := defaultTaskDaemonInterval
	if a.TaskDaemonInterval > 0 {
		interval = a.TaskDaemonInterval
	}
	fs.DurationVar(&interval, "interval", interval, "provider polling interval")
	if code := a.parseFlags(stdout, cfg.format, fs, args, taskDaemonHelp()); code >= 0 {
		return code
	}
	if interval < minimumTaskDaemonInterval || interval > maximumTaskDaemonInterval {
		a.usageError(stdout, cfg.format, "--interval must be between 250ms and 5m", fmt.Sprintf("Use `%s daemon --interval 2s`", a.taskUsageCommand(cfg)))
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

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastEvent []byte
	for {
		snapshot, err := a.taskDaemonPoll(ctx, provider)
		var event any = snapshot
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				return 0
			}
			fmt.Fprintf(stderr, "facets tasks daemon: refresh failed: %v\n", err)
			event = taskDaemonError{Type: "error", Message: err.Error(), Retrying: true}
		}

		encoded, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			fmt.Fprintf(stderr, "facets tasks daemon: encode event: %v\n", marshalErr)
			return 1
		}
		encoded = append(encoded, '\n')
		if !bytes.Equal(encoded, lastEvent) {
			if _, writeErr := stdout.Write(encoded); writeErr != nil {
				fmt.Fprintf(stderr, "facets tasks daemon: write event: %v\n", writeErr)
				return 1
			}
			lastEvent = append(lastEvent[:0], encoded...)
		}

		select {
		case <-ctx.Done():
			return 0
		case <-ticker.C:
		}
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

	snapshot := taskDaemonSnapshot{Type: "snapshot", Projects: make([]taskDaemonProject, 0, len(projects))}
	open := project.StatusOpen
	for _, item := range projects {
		registered, err := a.ProjectStore.RegisteredProject(ctx, provider.Name(), item.ID)
		if err != nil {
			return taskDaemonSnapshot{}, fmt.Errorf("read project %q metadata: %w", item.ID, err)
		}
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

		directory, _ := registered.Metadata["directory"].(string)
		daemonProject := taskDaemonProject{
			ID:        item.ID,
			Name:      item.Name,
			Directory: directory,
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
			})
		}
		snapshot.Projects = append(snapshot.Projects, daemonProject)
	}
	return snapshot, nil
}
