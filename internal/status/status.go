// Package status builds project task and recent-activity summaries.
package status

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"facets.barnlab.dev/internal/project"
	_ "modernc.org/sqlite"
)

const (
	codexSessionKey = "codex"
	ompSessionKey   = "omp"
	defaultPeriod   = 30
)

// UnknownSessionCount marks a session total that cannot be scoped to a project.
const UnknownSessionCount = -1

// TaskSummary contains task totals and priority buckets.
type TaskSummary struct {
	Total            int
	Open             int
	Closed           int
	OpenByPriority   map[int]int
	ClosedByPriority map[int]int
}

// Activity contains recent commits and session counts.
type Activity struct {
	Commits  int
	Sessions map[string]int
}

// Summary is the status of one project over a period.
type Summary struct {
	PeriodDays int
	Since      time.Time
	Tasks      TaskSummary
	Activity   Activity
}

// Builder creates project status summaries.
type Builder struct {
	Activity   ActivitySource
	Now        func() time.Time
	PeriodDays int
}

// ActivitySource supplies recent repository and session activity.
type ActivitySource interface {
	Summarize(context.Context, string, time.Time) (Activity, error)
}

// NewBuilder returns a builder using the local Codex, OMP, and jj activity source.
func NewBuilder() *Builder {
	return &Builder{
		Activity:   localActivitySource{},
		Now:        time.Now,
		PeriodDays: defaultPeriod,
	}
}

// Build lists all tasks for projectID and combines them with recent activity.
func (b *Builder) Build(ctx context.Context, provider project.Provider, root, projectID string) (Summary, error) {
	if ctx == nil {
		return Summary{}, errors.New("status: context is required")
	}
	if isNil(provider) {
		return Summary{}, errors.New("status: provider is required")
	}
	if b == nil {
		return Summary{}, errors.New("status: builder is required")
	}
	if isNil(b.Activity) {
		return Summary{}, errors.New("status: activity source is required")
	}
	root = strings.TrimSpace(root)
	if root != "" {
		resolved, err := workspaceRoot(root)
		if err != nil {
			return Summary{}, err
		}
		root = resolved
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return Summary{}, errors.New("status: project ID is required")
	}
	if b.PeriodDays <= 0 {
		return Summary{}, errors.New("status: period days must be positive")
	}
	if b.PeriodDays > int(math.MaxInt64/int64(24*time.Hour)) {
		return Summary{}, errors.New("status: period days is too large")
	}

	now := time.Now()
	if b.Now != nil {
		now = b.Now()
	}
	since := now.Add(-time.Duration(b.PeriodDays) * 24 * time.Hour)
	tasks, err := provider.ListTasks(ctx, projectID, project.TaskFilter{})
	if err != nil {
		return Summary{}, fmt.Errorf("status: list tasks: %w", err)
	}

	taskSummary := TaskSummary{
		OpenByPriority:   make(map[int]int),
		ClosedByPriority: make(map[int]int),
	}
	for _, task := range tasks {
		taskSummary.Total++
		switch task.Status {
		case project.StatusOpen:
			taskSummary.Open++
			if task.Priority != nil {
				taskSummary.OpenByPriority[*task.Priority]++
			}
		case project.StatusClosed:
			taskSummary.Closed++
			if task.Priority != nil {
				taskSummary.ClosedByPriority[*task.Priority]++
			}
		}
	}

	activity, err := b.Activity.Summarize(ctx, root, since)
	if err != nil {
		return Summary{}, fmt.Errorf("status: summarize activity: %w", err)
	}
	activity = normalizeActivity(activity)
	return Summary{
		PeriodDays: b.PeriodDays,
		Since:      since,
		Tasks:      taskSummary,
		Activity:   activity,
	}, nil
}

func normalizeActivity(activity Activity) Activity {
	return Activity{
		Commits: activity.Commits,
		Sessions: map[string]int{
			codexSessionKey: activity.Sessions[codexSessionKey],
			ompSessionKey:   activity.Sessions[ompSessionKey],
		},
	}
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func workspaceRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("status: workspace root is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("status: resolve workspace root: %w", err)
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("status: stat workspace root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("status: workspace root is not a directory: %s", root)
	}
	for candidate := root; ; candidate = filepath.Dir(candidate) {
		for _, marker := range []string{".jj", ".git"} {
			if markerInfo, markerErr := os.Stat(filepath.Join(candidate, marker)); markerErr == nil && (markerInfo.IsDir() || marker == ".git") {
				return candidate, nil
			}
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return root, nil
		}
	}
}

type localActivitySource struct{}

func (localActivitySource) Summarize(ctx context.Context, root string, since time.Time) (Activity, error) {
	if ctx == nil {
		return Activity{}, errors.New("status: context is required")
	}
	if strings.TrimSpace(root) == "" {
		return Activity{Sessions: map[string]int{ompSessionKey: UnknownSessionCount}}, nil
	}
	root, err := workspaceRoot(root)
	if err != nil {
		return Activity{}, err
	}

	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	ompHome := strings.TrimSpace(os.Getenv("OMP_HOME"))
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		if codexHome == "" {
			codexHome = filepath.Join(home, ".codex")
		}
		if ompHome == "" {
			ompHome = filepath.Join(home, ".omp")
		}
	}
	codex, err := countSessionDatabase(ctx, codexHome, "state_5.sqlite", "threads", "created_at_ms", "", root, since)
	if err != nil {
		return Activity{}, fmt.Errorf("status: count Codex sessions: %w", err)
	}
	omp, err := countSessionDatabaseCandidates(ctx, ompHome, []string{
		filepath.Join("agent", "history.db"),
		filepath.Join("agent", "history"),
	}, "history", "created_at", "session_id", root, since)
	if err != nil {
		return Activity{}, fmt.Errorf("status: count OMP sessions: %w", err)
	}
	commits, err := countCommits(ctx, root, since)
	if err != nil {
		return Activity{}, err
	}
	return Activity{Commits: commits, Sessions: map[string]int{
		codexSessionKey: codex,
		ompSessionKey:   omp,
	}}, nil
}

func countSessionDatabaseCandidates(ctx context.Context, home string, databaseNames []string, table, timestampColumn, sessionColumn, root string, since time.Time) (int, error) {
	for _, databaseName := range databaseNames {
		path := filepath.Join(strings.TrimSpace(home), databaseName)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, fmt.Errorf("stat %s: %w", path, err)
		}
		return countSessionDatabase(ctx, home, databaseName, table, timestampColumn, sessionColumn, root, since)
	}
	return 0, nil
}

func countSessionDatabase(ctx context.Context, home, databaseName, table, timestampColumn, sessionColumn, root string, since time.Time) (int, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return 0, nil
	}
	path := filepath.Join(home, databaseName)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		return 0, fmt.Errorf("connect %s: %w", path, err)
	}

	query := "SELECT cwd, " + timestampColumn
	if sessionColumn != "" {
		query += ", " + sessionColumn
	}
	query += " FROM " + table
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("query %s: %w", path, err)
	}
	defer rows.Close()
	count := 0
	var seenSessions map[string]struct{}
	if sessionColumn != "" {
		seenSessions = make(map[string]struct{})
	}
	for rows.Next() {
		var cwd string
		var rawCreatedAt any
		var sessionID string
		var scanErr error
		if sessionColumn == "" {
			scanErr = rows.Scan(&cwd, &rawCreatedAt)
		} else {
			scanErr = rows.Scan(&cwd, &rawCreatedAt, &sessionID)
		}
		if scanErr != nil {
			return 0, fmt.Errorf("scan %s: %w", path, scanErr)
		}
		createdAt, err := parseTimestamp(rawCreatedAt)
		if err != nil {
			return 0, fmt.Errorf("parse created_at in %s: %w", path, err)
		}
		if createdAt.Before(since) || !pathWithinRoot(root, cwd) {
			continue
		}
		if sessionColumn != "" {
			if _, exists := seenSessions[sessionID]; exists {
				continue
			}
			seenSessions[sessionID] = struct{}{}
		}
		count++
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close %s: %w", path, err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate %s: %w", path, err)
	}
	return count, nil
}

func pathWithinRoot(root, candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return false
	}
	candidate, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, filepath.Clean(candidate))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}

func countCommits(ctx context.Context, root string, since time.Time) (int, error) {
	command := []string{"git", "log", "--all", "--format=%cI"}
	if markerInfo, err := os.Stat(filepath.Join(root, ".jj")); err == nil && markerInfo.IsDir() {
		command = []string{"jj", "log", "--ignore-working-copy", "--no-graph", "-r", "all()", "-T", `committer.timestamp() ++ "\n"`}
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return 0, fmt.Errorf("status: %s log failed: %w: %s", command[0], err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return 0, fmt.Errorf("status: %s log failed: %w", command[0], err)
	}

	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		timestamp, err := parseTimestamp(line)
		if err != nil {
			return 0, fmt.Errorf("status: parse %s timestamp %q: %w", command[0], line, err)
		}
		if !timestamp.Before(since) {
			count++
		}
	}
	return count, nil
}

func parseTimestamp(value any) (time.Time, error) {
	switch value := value.(type) {
	case time.Time:
		return value, nil
	case int64:
		return unixTimestamp(value), nil
	case int32:
		return unixTimestamp(int64(value)), nil
	case int:
		return unixTimestamp(int64(value)), nil
	case uint64:
		if value > math.MaxInt64 {
			return time.Time{}, errors.New("timestamp is out of range")
		}
		return unixTimestamp(int64(value)), nil
	case float64:
		return numericTimestamp(value)
	case []byte:
		return parseTimestampString(string(value))
	case string:
		return parseTimestampString(value)
	case nil:
		return time.Time{}, errors.New("timestamp is NULL")
	default:
		return time.Time{}, fmt.Errorf("unsupported timestamp type %T", value)
	}
}
func parseTimestampString(value string) (time.Time, error) {
	if numeric, err := strconv.ParseFloat(value, 64); err == nil {
		return numericTimestamp(numeric)
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999 -07:00",
		"2006-01-02 15:04:05 -07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q", value)
}

func numericTimestamp(value float64) (time.Time, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > math.MaxInt64 {
		return time.Time{}, errors.New("timestamp is out of range")
	}
	magnitude := math.Abs(value)
	var seconds, nanos int64
	switch {
	case magnitude >= 1e18:
		seconds = int64(value / 1e9)
		nanos = int64(value) % 1e9
	case magnitude >= 1e15:
		seconds = int64(value / 1e6)
		nanos = (int64(value) % 1e6) * 1e3
	case magnitude >= 1e12:
		seconds = int64(value / 1e3)
		nanos = (int64(value) % 1e3) * 1e6
	default:
		seconds = int64(value)
		nanos = int64((value - float64(seconds)) * 1e9)
	}
	return time.Unix(seconds, nanos), nil
}

func unixTimestamp(value int64) time.Time {
	magnitude := value
	if magnitude < 0 {
		magnitude = -magnitude
	}
	switch {
	case magnitude >= 1e18:
		return time.Unix(value/1e9, value%1e9)
	case magnitude >= 1e15:
		return time.Unix(value/1e6, (value%1e6)*1e3)
	case magnitude >= 1e12:
		return time.Unix(value/1e3, (value%1e3)*1e6)
	default:
		return time.Unix(value, 0)
	}
}
