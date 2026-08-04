package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProjectLifecyclePersists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "facets.db")
	store := openTestStore(t, ctx, path)

	created, err := store.CreateProject(ctx, ProjectInput{Name: "  Facets  ", Slug: " facets "})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if created.ID == 0 || created.Name != "Facets" || created.Slug != "facets" {
		t.Fatalf("CreateProject() = %#v", created)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("CreateProject() timestamps = %v, %v", created.CreatedAt, created.UpdatedAt)
	}

	bySlug, err := store.ProjectBySlug(ctx, " facets ")
	if err != nil {
		t.Fatalf("ProjectBySlug() error = %v", err)
	}
	if bySlug != created {
		t.Fatalf("ProjectBySlug() = %#v, want %#v", bySlug, created)
	}

	updated, err := store.UpdateProject(ctx, created.ID, ProjectInput{Name: "Facets Dashboard", Slug: "dashboard"})
	if err != nil {
		t.Fatalf("UpdateProject() error = %v", err)
	}
	if updated.Name != "Facets Dashboard" || updated.Slug != "dashboard" {
		t.Fatalf("UpdateProject() = %#v", updated)
	}
	if _, err := store.ProjectBySlug(ctx, "facets"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ProjectBySlug(old slug) error = %v, want ErrNotFound", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	store = openTestStore(t, ctx, path)

	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projects) != 1 || projects[0] != updated {
		t.Fatalf("ListProjects() = %#v, want [%#v]", projects, updated)
	}

	if err := store.DeleteProject(ctx, created.ID); err != nil {
		t.Fatalf("DeleteProject() error = %v", err)
	}
	if err := store.DeleteProject(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteProject(missing) error = %v, want ErrNotFound", err)
	}
}

func TestProjectValidationAndUniqueness(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "facets.db"))

	for _, input := range []ProjectInput{
		{Name: "", Slug: "valid"},
		{Name: "Valid", Slug: ""},
	} {
		if _, err := store.CreateProject(ctx, input); err == nil {
			t.Fatalf("CreateProject(%#v) error = nil", input)
		}
	}

	if _, err := store.CreateProject(ctx, ProjectInput{Name: "One", Slug: "same"}); err != nil {
		t.Fatalf("CreateProject(first) error = %v", err)
	}
	if _, err := store.CreateProject(ctx, ProjectInput{Name: "Two", Slug: "same"}); err == nil {
		t.Fatal("CreateProject(duplicate slug) error = nil")
	}
	if _, err := store.UpdateProject(ctx, 999, ProjectInput{Name: "Missing", Slug: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateProject(missing) error = %v, want ErrNotFound", err)
	}
}

func TestConcurrentUpdatesReturnTheirOwnValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "facets.db"))
	project, err := store.CreateProject(ctx, ProjectInput{Name: "Initial", Slug: "initial"})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}

	const updateCount = 32
	start := make(chan struct{})
	errs := make(chan error, updateCount)
	var workers sync.WaitGroup
	for i := range updateCount {
		name := fmt.Sprintf("Project %d", i)
		slug := fmt.Sprintf("project-%d", i)
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			updated, err := store.UpdateProject(ctx, project.ID, ProjectInput{Name: name, Slug: slug})
			if err != nil {
				errs <- err
				return
			}
			if updated.Name != name || updated.Slug != slug {
				errs <- fmt.Errorf("UpdateProject() = %q/%q, want %q/%q", updated.Name, updated.Slug, name, slug)
			}
		}()
	}

	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestConcurrentOpenMigratesOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "facets.db")
	const openerCount = 8
	start := make(chan struct{})
	errs := make(chan error, openerCount)
	var workers sync.WaitGroup
	for range openerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			store, err := Open(ctx, path)
			if err == nil {
				err = store.Close()
			}
			errs <- err
		}()
	}

	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Open() error = %v", err)
		}
	}
}

func TestConnectionSettingsSurviveReplacement(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "facets.db"))
	store.db.SetConnMaxLifetime(time.Nanosecond)
	time.Sleep(time.Millisecond)

	var foreignKeys, busyTimeout int
	if err := store.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys error = %v", err)
	}
	if err := store.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy_timeout error = %v", err)
	}
	if foreignKeys != 1 || busyTimeout != 5000 {
		t.Fatalf("replacement connection settings = foreign_keys:%d busy_timeout:%d", foreignKeys, busyTimeout)
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode = DELETE").Scan(&journalMode); err != nil {
		t.Fatalf("set journal_mode error = %v", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 2"); err != nil {
		t.Fatalf("set user_version error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, err = Open(ctx, path)
	if err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("Open() error = %v, want newer-schema error", err)
	}

	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen future database error = %v", err)
	}
	defer db.Close()
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode error = %v", err)
	}
	if journalMode != "delete" {
		t.Fatalf("journal_mode = %q, want database unchanged in delete mode", journalMode)
	}
}

func TestOpenRejectsNegativeSchemaVersion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "invalid.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = -1"); err != nil {
		t.Fatalf("set user_version error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, err = Open(ctx, path)
	if err == nil || !strings.Contains(err.Error(), "invalid database schema version -1") {
		t.Fatalf("Open() error = %v, want invalid-schema error", err)
	}
}

func openTestStore(t *testing.T, ctx context.Context, path string) *Store {
	t.Helper()
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
