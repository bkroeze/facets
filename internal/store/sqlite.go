package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"facets.barnlab.dev/internal/project"
	_ "modernc.org/sqlite"
)

const schemaVersion = 5

var ErrNotFound = errors.New("store: not found")

// Store persists Facets data in SQLite.
type Store struct {
	db *sql.DB
}

// Project is a manually registered project.
type Project struct {
	CreatedAt time.Time
	UpdatedAt time.Time
	Name      string
	Slug      string
	ID        int64
}

// RegisteredProject is a provider project tracked by the local registry.
type RegisteredProject struct {
	FirstSeen  time.Time
	LastSeen   time.Time
	Metadata   map[string]any
	DisabledAt *time.Time
	Source     string
	ID         string
	Name       string
}

// Directory returns normalized directory metadata when it is a non-empty string.
func (p RegisteredProject) Directory() (string, bool) {
	directory, ok := p.Metadata["directory"].(string)
	directory = strings.TrimSpace(directory)
	return directory, ok && directory != ""
}

// DayFocus is a focus entry assigned to a user's local calendar day.
type DayFocus struct {
	DayStart  time.Time
	CreatedAt time.Time
	Focus     string
	ID        int64
}

// ProjectInput contains the mutable fields of a project.
type ProjectInput struct {
	Name string
	Slug string
}

// DefaultPath returns the default local registry database path.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("store: resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "facets", "facets.db"), nil
}

// Open opens a SQLite database and applies all available schema migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("store: database path is required")
	}
	if parent := filepath.Dir(path); parent != "." && parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return nil, fmt.Errorf("store: create database directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", connectionDSN(path))
	if err != nil {
		return nil, fmt.Errorf("store: open database: %w", err)
	}

	// A single pooled connection preserves :memory: data. Connection-local
	// safety settings are also encoded in the DSN, so replacement connections
	// receive the same configuration.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	closeOnError := func(err error) (*Store, error) {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("%w (also closing database: %v)", err, closeErr)
		}
		return nil, err
	}

	if err := db.PingContext(ctx); err != nil {
		return closeOnError(fmt.Errorf("store: connect to database: %w", err))
	}
	if err := migrate(ctx, db); err != nil {
		return closeOnError(err)
	}

	return &Store{db: db}, nil
}

func connectionDSN(path string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	if strings.HasSuffix(path, "?") || strings.HasSuffix(path, "&") {
		separator = ""
	}
	return path + separator + "_pragma=foreign_keys%3Don&_pragma=busy_timeout%3D5000&_txlock=immediate"
}

func migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	if version < 0 {
		return fmt.Errorf("store: invalid database schema version %d", version)
	}
	if version > schemaVersion {
		return fmt.Errorf("store: database schema version %d is newer than supported version %d", version, schemaVersion)
	}

	if version == 0 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE projects (
				id INTEGER PRIMARY KEY,
				name TEXT NOT NULL CHECK (length(trim(name)) > 0),
				slug TEXT NOT NULL UNIQUE CHECK (length(trim(slug)) > 0),
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			) STRICT;
			PRAGMA user_version = 1;
		`); err != nil {
			return fmt.Errorf("store: migrate schema to version 1: %w", err)
		}
		version = 1
	}
	if version < 2 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE project_registry (
				source TEXT NOT NULL CHECK (length(trim(source)) > 0),
				project_id TEXT NOT NULL CHECK (length(trim(project_id)) > 0),
				name TEXT NOT NULL CHECK (length(trim(name)) > 0),
				metadata TEXT NOT NULL DEFAULT '{}',
				first_seen INTEGER NOT NULL,
				last_seen INTEGER NOT NULL,
				PRIMARY KEY (source, project_id)
			) STRICT;
			PRAGMA user_version = 2;
		`); err != nil {
			return fmt.Errorf("store: migrate schema to version 2: %w", err)
		}
	}
	if version < 3 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE saved_views (
				id TEXT PRIMARY KEY CHECK (length(id) > 0),
				name TEXT NOT NULL COLLATE NOCASE UNIQUE CHECK (length(trim(name)) > 0),
				query_json TEXT NOT NULL,
				order_json TEXT NOT NULL,
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			) STRICT;
			PRAGMA user_version = 3;
		`); err != nil {
			return fmt.Errorf("store: migrate schema to version 3: %w", err)
		}
	}
	if version < 4 {
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE project_registry ADD COLUMN disabled_at INTEGER;
			PRAGMA user_version = 4;
		`); err != nil {
			return fmt.Errorf("store: migrate schema to version 4: %w", err)
		}
	}
	if version < 5 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE day_focus (
				id INTEGER PRIMARY KEY,
				focus TEXT NOT NULL CHECK (length(trim(focus)) > 0),
				day_start INTEGER NOT NULL,
				created_at INTEGER NOT NULL
			) STRICT;
			CREATE INDEX day_focus_day_start_created_at_idx
				ON day_focus (day_start, created_at, id);
			PRAGMA user_version = 5;
		`); err != nil {
			return fmt.Errorf("store: migrate schema to version 5: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration: %w", err)
	}
	return nil
}

// Close releases the database connection.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("store: close database: %w", err)
	}
	return nil
}

// CreateProject registers a project.
func (s *Store) CreateProject(ctx context.Context, input ProjectInput) (Project, error) {
	input, err := cleanProjectInput(input)
	if err != nil {
		return Project{}, err
	}

	now := time.Now().UTC().UnixMilli()
	project, err := scanProject(s.db.QueryRowContext(ctx, `
		INSERT INTO projects (name, slug, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		RETURNING id, name, slug, created_at, updated_at
	`, input.Name, input.Slug, now, now))
	if err != nil {
		return Project{}, fmt.Errorf("store: create project: %w", err)
	}
	return project, nil
}

// ProjectByID returns the project with id.
func (s *Store) ProjectByID(ctx context.Context, id int64) (Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, `
		SELECT id, name, slug, created_at, updated_at
		FROM projects
		WHERE id = ?
	`, id))
}

// ProjectBySlug returns the project with slug.
func (s *Store) ProjectBySlug(ctx context.Context, slug string) (Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, `
		SELECT id, name, slug, created_at, updated_at
		FROM projects
		WHERE slug = ?
	`, strings.TrimSpace(slug)))
}

// ListProjects returns every project in stable display order.
func (s *Store) ListProjects(ctx context.Context) (projects []Project, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, slug, created_at, updated_at
		FROM projects
		ORDER BY name COLLATE NOCASE, id
	`)
	if err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("store: list projects: close rows: %w", closeErr)
		}
	}()

	projects = make([]Project, 0)

	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	return projects, nil
}

// UpdateProject replaces the mutable fields of an existing project.
func (s *Store) UpdateProject(ctx context.Context, id int64, input ProjectInput) (Project, error) {
	input, err := cleanProjectInput(input)
	if err != nil {
		return Project{}, err
	}

	project, err := scanProject(s.db.QueryRowContext(ctx, `
		UPDATE projects
		SET name = ?, slug = ?, updated_at = ?
		WHERE id = ?
		RETURNING id, name, slug, created_at, updated_at
	`, input.Name, input.Slug, time.Now().UTC().UnixMilli(), id))
	if err != nil {
		return Project{}, fmt.Errorf("store: update project: %w", err)
	}
	return project, nil
}

// DeleteProject removes a project.
func (s *Store) DeleteProject(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM projects WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete project: %w", err)
	}
	return requireChangedRow(result)
}

// SyncProjects records the latest provider project list while preserving
// locally configured directory metadata and first-seen timestamps.
func (s *Store) SyncProjects(ctx context.Context, source string, items []project.Project) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return errors.New("store: project source is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin project sync: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC().UnixMilli()
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		name := strings.TrimSpace(item.Name)
		if id == "" {
			return errors.New("store: project ID is required")
		}
		if name == "" {
			return fmt.Errorf("store: project %q name is required", id)
		}
		metadata, err := providerMetadata(tx, source, id, item.Metadata)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("store: encode metadata for project %q: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO project_registry (source, project_id, name, metadata, first_seen, last_seen)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(source, project_id) DO UPDATE SET
				name = excluded.name,
				metadata = excluded.metadata,
				last_seen = excluded.last_seen
		`, source, id, name, string(encoded), now, now); err != nil {
			return fmt.Errorf("store: sync project %q: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit project sync: %w", err)
	}
	return nil
}

// RegisteredProjects returns every provider project for source in stable ID order.
func (s *Store) RegisteredProjects(ctx context.Context, source string) (projects []RegisteredProject, err error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, errors.New("store: project source is required")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT source, project_id, name, metadata, first_seen, last_seen, disabled_at
		FROM project_registry
		WHERE source = ?
		ORDER BY project_id
	`, source)
	if err != nil {
		return nil, fmt.Errorf("store: list registered projects: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("store: list registered projects: close rows: %w", closeErr)
		}
	}()

	projects = make([]RegisteredProject, 0)

	for rows.Next() {
		registered, err := scanRegisteredProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, registered)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list registered projects: %w", err)
	}
	return projects, nil
}

// RegisteredProject returns a provider project from the local registry.
func (s *Store) RegisteredProject(ctx context.Context, source, id string) (RegisteredProject, error) {
	source, id, err := cleanRegisteredProjectIdentity(source, id)
	if err != nil {
		return RegisteredProject{}, err
	}
	return s.registeredProject(ctx, source, id)
}

func (s *Store) registeredProject(ctx context.Context, source, id string) (RegisteredProject, error) {
	return scanRegisteredProject(s.db.QueryRowContext(ctx, `
		SELECT source, project_id, name, metadata, first_seen, last_seen, disabled_at
		FROM project_registry
		WHERE source = ? AND project_id = ?
	`, source, id))
}

// SetProjectMetadata sets one local metadata key on a registered project.
func (s *Store) SetProjectMetadata(ctx context.Context, source, id, key, value string) (RegisteredProject, error) {
	source, id, err := cleanRegisteredProjectIdentity(source, id)
	if err != nil {
		return RegisteredProject{}, err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return RegisteredProject{}, errors.New("store: project metadata key is required")
	}
	current, err := s.registeredProject(ctx, source, id)
	if err != nil {
		return RegisteredProject{}, err
	}
	if current.Metadata == nil {
		current.Metadata = make(map[string]any)
	}
	current.Metadata[key] = value
	encoded, err := json.Marshal(current.Metadata)
	if err != nil {
		return RegisteredProject{}, fmt.Errorf("store: encode project metadata: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE project_registry
		SET metadata = ?
		WHERE source = ? AND project_id = ?
	`, string(encoded), source, id); err != nil {
		return RegisteredProject{}, fmt.Errorf("store: set project metadata: %w", err)
	}
	current.Metadata = cloneMetadata(current.Metadata)
	return current, nil
}

// SetProjectDisabled records whether a registered project is inactive. A
// disable timestamp is preserved across repeated disables and cleared on
// enable so historical registry data remains intact.
func (s *Store) SetProjectDisabled(ctx context.Context, source, id string, disabled bool) (RegisteredProject, error) {
	source, id, err := cleanRegisteredProjectIdentity(source, id)
	if err != nil {
		return RegisteredProject{}, err
	}

	now := time.Now().UTC().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE project_registry
		SET disabled_at = CASE WHEN ? THEN COALESCE(disabled_at, ?) ELSE NULL END
		WHERE source = ? AND project_id = ?
	`, disabled, now, source, id); err != nil {
		return RegisteredProject{}, fmt.Errorf("store: set project disabled state: %w", err)
	}
	return s.registeredProject(ctx, source, id)
}

func cleanRegisteredProjectIdentity(source, id string) (string, string, error) {
	source = strings.TrimSpace(source)
	id = strings.TrimSpace(id)
	if source == "" {
		return "", "", errors.New("store: project source is required")
	}
	if id == "" {
		return "", "", errors.New("store: project ID is required")
	}
	return source, id, nil
}

// CreateDayFocus stores a focus entry for the local calendar day containing createdAt.
// Entries are append-only; the latest entry for a day is the current focus.
func (s *Store) CreateDayFocus(ctx context.Context, focus string, createdAt time.Time) (DayFocus, error) {
	focus = strings.TrimSpace(focus)
	if focus == "" {
		return DayFocus{}, errors.New("store: day focus is required")
	}
	if createdAt.IsZero() {
		return DayFocus{}, errors.New("store: day focus timestamp is required")
	}
	dayStart := localDayStart(createdAt)
	return scanDayFocus(s.db.QueryRowContext(ctx, `
		INSERT INTO day_focus (focus, day_start, created_at)
		VALUES (?, ?, ?)
		RETURNING id, focus, day_start, created_at
	`, focus, dayStart.UnixMilli(), createdAt.UTC().UnixMilli()))
}

// CurrentDayFocus returns the latest focus entry for the local calendar day
// containing now. The location carried by now defines the day boundary.
func (s *Store) CurrentDayFocus(ctx context.Context, now time.Time) (DayFocus, error) {
	if now.IsZero() {
		return DayFocus{}, errors.New("store: current day timestamp is required")
	}
	return scanDayFocus(s.db.QueryRowContext(ctx, `
		SELECT id, focus, day_start, created_at
		FROM day_focus
		WHERE day_start = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, localDayStart(now).UnixMilli()))
}

// ListSavedViews returns persisted user-defined views. Built-in views are not stored.
func (s *Store) ListSavedViews(ctx context.Context) (views []project.SavedView, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, query_json, order_json, created_at, updated_at
		FROM saved_views
		ORDER BY name COLLATE NOCASE, id
	`)
	if err != nil {
		return nil, fmt.Errorf("store: list saved views: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("store: list saved views: close rows: %w", closeErr)
		}
	}()
	views = make([]project.SavedView, 0)
	for rows.Next() {
		view, err := scanSavedView(rows)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list saved views: %w", err)
	}
	return views, nil
}

// GetSavedView returns one persisted user-defined view.
func (s *Store) GetSavedView(ctx context.Context, id string) (project.SavedView, error) {
	if id == "" {
		return project.SavedView{}, errors.New("store: saved view ID is required")
	}
	return scanSavedView(s.db.QueryRowContext(ctx, `
		SELECT id, name, query_json, order_json, created_at, updated_at
		FROM saved_views
		WHERE id = ?
	`, id))
}

// CreateSavedView persists one user-defined view.
func (s *Store) CreateSavedView(ctx context.Context, view project.SavedView) (project.SavedView, error) {
	if view.ID == "" {
		return project.SavedView{}, errors.New("store: saved view ID is required")
	}
	view.Name = strings.TrimSpace(view.Name)
	if view.Name == "" {
		return project.SavedView{}, errors.New("store: saved view name is required")
	}
	queryJSON, orderJSON, err := encodeSavedView(view.Query, view.Order)
	if err != nil {
		return project.SavedView{}, err
	}
	now := time.Now().UTC().UnixMilli()
	created, err := scanSavedView(s.db.QueryRowContext(ctx, `
		INSERT INTO saved_views (id, name, query_json, order_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING id, name, query_json, order_json, created_at, updated_at
	`, view.ID, view.Name, queryJSON, orderJSON, now, now))
	if err != nil {
		if isUniqueConstraint(err) {
			return project.SavedView{}, project.ErrConflict
		}
		return project.SavedView{}, err
	}
	return created, nil
}

// UpdateSavedView replaces the supplied fields on one persisted view.
func (s *Store) UpdateSavedView(ctx context.Context, id string, patch project.SavedViewPatch) (project.SavedView, error) {
	if id == "" {
		return project.SavedView{}, errors.New("store: saved view ID is required")
	}
	set := make([]string, 0, 4)
	args := make([]any, 0, 5)
	if patch.Name != nil {
		set = append(set, "name = ?")
		args = append(args, *patch.Name)
	}
	if patch.Query != nil {
		encoded, err := json.Marshal(*patch.Query)
		if err != nil {
			return project.SavedView{}, fmt.Errorf("store: encode saved view query: %w", err)
		}
		set = append(set, "query_json = ?")
		args = append(args, string(encoded))
	}
	if patch.Order != nil {
		encoded, err := json.Marshal(*patch.Order)
		if err != nil {
			return project.SavedView{}, fmt.Errorf("store: encode saved view order: %w", err)
		}
		set = append(set, "order_json = ?")
		args = append(args, string(encoded))
	}
	if len(set) == 0 {
		return project.SavedView{}, errors.New("store: saved view patch must contain a change")
	}
	set = append(set, "updated_at = ?")
	args = append(args, time.Now().UTC().UnixMilli(), id)
	statement := `
		UPDATE saved_views
		SET ` + strings.Join(set, ", ") + `
		WHERE id = ?
		RETURNING id, name, query_json, order_json, created_at, updated_at
	`
	view, err := scanSavedView(s.db.QueryRowContext(ctx, statement, args...))
	if err != nil {
		if isUniqueConstraint(err) {
			return project.SavedView{}, project.ErrConflict
		}
		return project.SavedView{}, err
	}
	return view, nil
}

func localDayStart(value time.Time) time.Time {
	local := value.In(value.Location())
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
}

func scanDayFocus(row rowScanner) (DayFocus, error) {
	var (
		focus               DayFocus
		dayStart, createdAt int64
	)
	if err := row.Scan(&focus.ID, &focus.Focus, &dayStart, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DayFocus{}, ErrNotFound
		}
		return DayFocus{}, fmt.Errorf("store: read day focus: %w", err)
	}
	focus.DayStart = time.UnixMilli(dayStart).UTC()
	focus.CreatedAt = time.UnixMilli(createdAt).UTC()
	return focus, nil
}

// DeleteSavedView removes one persisted user-defined view.
func (s *Store) DeleteSavedView(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("store: saved view ID is required")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM saved_views WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete saved view: %w", err)
	}
	if err := requireChangedRow(result); err != nil {
		if errors.Is(err, ErrNotFound) {
			return project.ErrNotFound
		}
		return err
	}
	return nil
}

func encodeSavedView(query project.TaskQuery, order project.TaskOrder) (string, string, error) {
	queryJSON, err := json.Marshal(query)
	if err != nil {
		return "", "", fmt.Errorf("store: encode saved view query: %w", err)
	}
	orderJSON, err := json.Marshal(order)
	if err != nil {
		return "", "", fmt.Errorf("store: encode saved view order: %w", err)
	}
	return string(queryJSON), string(orderJSON), nil
}

func scanSavedView(row rowScanner) (project.SavedView, error) {
	var (
		view                 project.SavedView
		queryJSON, orderJSON string
		createdAt, updatedAt int64
	)
	if err := row.Scan(&view.ID, &view.Name, &queryJSON, &orderJSON, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return project.SavedView{}, project.ErrNotFound
		}
		return project.SavedView{}, fmt.Errorf("store: read saved view: %w", err)
	}
	if err := json.Unmarshal([]byte(queryJSON), &view.Query); err != nil {
		return project.SavedView{}, fmt.Errorf("store: decode saved view query: %w", err)
	}
	if err := json.Unmarshal([]byte(orderJSON), &view.Order); err != nil {
		return project.SavedView{}, fmt.Errorf("store: decode saved view order: %w", err)
	}
	view.CreatedAt = time.UnixMilli(createdAt).UTC()
	view.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return view, nil
}

func isUniqueConstraint(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func providerMetadata(tx *sql.Tx, source, id string, incoming map[string]any) (map[string]any, error) {
	metadata := cloneMetadata(incoming)
	var existing string
	err := tx.QueryRow(`
		SELECT metadata
		FROM project_registry
		WHERE source = ? AND project_id = ?
	`, source, id).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		return metadata, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: read metadata for project %q: %w", id, err)
	}
	var current map[string]any
	if existing != "" {
		if err := json.Unmarshal([]byte(existing), &current); err != nil {
			return nil, fmt.Errorf("store: decode metadata for project %q: %w", id, err)
		}
	}
	if directory, ok := current["directory"]; ok {
		metadata["directory"] = directory
	}
	return metadata, nil
}

func cloneMetadata(source map[string]any) map[string]any {
	if len(source) == 0 {
		return make(map[string]any)
	}
	target := make(map[string]any, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

func cleanProjectInput(input ProjectInput) (ProjectInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.TrimSpace(input.Slug)
	if input.Name == "" {
		return ProjectInput{}, errors.New("store: project name is required")
	}
	if input.Slug == "" {
		return ProjectInput{}, errors.New("store: project slug is required")
	}
	return input, nil
}

func scanRegisteredProject(row rowScanner) (RegisteredProject, error) {
	var (
		registered          RegisteredProject
		metadata            string
		firstSeen, lastSeen int64
		disabledAt          sql.NullInt64
	)
	if err := row.Scan(&registered.Source, &registered.ID, &registered.Name, &metadata, &firstSeen, &lastSeen, &disabledAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RegisteredProject{}, ErrNotFound
		}
		return RegisteredProject{}, fmt.Errorf("store: read registered project: %w", err)
	}
	if metadata != "" {
		if err := json.Unmarshal([]byte(metadata), &registered.Metadata); err != nil {
			return RegisteredProject{}, fmt.Errorf("store: decode registered project metadata: %w", err)
		}
	}
	if registered.Metadata == nil {
		registered.Metadata = make(map[string]any)
	}
	registered.FirstSeen = time.UnixMilli(firstSeen).UTC()
	registered.LastSeen = time.UnixMilli(lastSeen).UTC()
	if disabledAt.Valid {
		value := time.UnixMilli(disabledAt.Int64).UTC()
		registered.DisabledAt = &value
	}
	return registered, nil
}

func requireChangedRow(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: read affected rows: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProject(row rowScanner) (Project, error) {
	var (
		project              Project
		createdAt, updatedAt int64
	)
	if err := row.Scan(&project.ID, &project.Name, &project.Slug, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Project{}, ErrNotFound
		}
		return Project{}, fmt.Errorf("store: read project: %w", err)
	}
	project.CreatedAt = time.UnixMilli(createdAt).UTC()
	project.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return project, nil
}
