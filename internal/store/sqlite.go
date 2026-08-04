package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

var ErrNotFound = errors.New("store: not found")

// Store persists Facets data in SQLite.
type Store struct {
	db *sql.DB
}

// Project is a manually registered project.
type Project struct {
	ID        int64
	Name      string
	Slug      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ProjectInput contains the mutable fields of a project.
type ProjectInput struct {
	Name string
	Slug string
}

// Open opens a SQLite database and applies all available schema migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("store: database path is required")
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
		_ = db.Close()
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
func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, slug, created_at, updated_at
		FROM projects
		ORDER BY name COLLATE NOCASE, id
	`)
	if err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	defer rows.Close()

	projects := make([]Project, 0)
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
