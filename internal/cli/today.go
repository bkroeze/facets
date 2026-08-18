package cli

import (
	"context"
	"fmt"
	"sort"
	"time"

	"facets.barnlab.dev/internal/project"
)

type topTask struct {
	Project project.Project
	Task    project.Task
}

type completionMetrics struct {
	DayStart time.Time
	DayEnd   time.Time
	All      int
	Top      int
}

// listTopTasks returns open tasks marked with the exact boolean metadata value
// facets.top=true. Missing or malformed metadata is ignored.
func listTopTasks(ctx context.Context, provider project.Provider) ([]topTask, error) {
	projects, err := provider.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	status := project.StatusOpen
	tasks := make([]topTask, 0)
	for _, item := range projects {
		items, err := provider.ListTasks(ctx, item.ID, project.TaskFilter{Status: &status})
		if err != nil {
			return nil, fmt.Errorf("list open tasks for project %q: %w", item.ID, err)
		}
		for _, task := range items {
			if task.Status != project.StatusOpen || !isTopTask(task) {
				continue
			}
			if task.ProjectID == "" {
				task.ProjectID = item.ID
			}
			tasks = append(tasks, topTask{Project: item, Task: task})
		}
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Project.ID != tasks[j].Project.ID {
			return tasks[i].Project.ID < tasks[j].Project.ID
		}
		if tasks[i].Task.UpdatedAt.Equal(tasks[j].Task.UpdatedAt) {
			return tasks[i].Task.ID < tasks[j].Task.ID
		}
		return tasks[i].Task.UpdatedAt.After(tasks[j].Task.UpdatedAt)
	})
	return tasks, nil
}

func isTopTask(task project.Task) bool {
	value, ok := task.Metadata["facets.top"]
	top, ok := value.(bool)
	return ok && top
}

// completedToday counts closed tasks whose UpdatedAt falls within the local
// calendar day represented by now. UpdatedAt is the provider's completion
// timestamp; open tasks and malformed or missing facets.top metadata are not
// counted as top tasks.
func completedToday(ctx context.Context, provider project.Provider, now time.Time) (completionMetrics, error) {
	if now.IsZero() {
		return completionMetrics{}, fmt.Errorf("current time is required")
	}
	local := now.In(now.Location())
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	dayEnd := dayStart.AddDate(0, 0, 1)
	projects, err := provider.ListProjects(ctx)
	if err != nil {
		return completionMetrics{}, fmt.Errorf("list projects: %w", err)
	}
	status := project.StatusClosed
	metrics := completionMetrics{DayStart: dayStart, DayEnd: dayEnd}
	for _, item := range projects {
		tasks, err := provider.ListTasks(ctx, item.ID, project.TaskFilter{Status: &status})
		if err != nil {
			return completionMetrics{}, fmt.Errorf("list closed tasks for project %q: %w", item.ID, err)
		}
		for _, task := range tasks {
			if task.Status != project.StatusClosed || task.UpdatedAt.Before(dayStart) || !task.UpdatedAt.Before(dayEnd) {
				continue
			}
			metrics.All++
			if isTopTask(task) {
				metrics.Top++
			}
		}
	}
	return metrics, nil
}
