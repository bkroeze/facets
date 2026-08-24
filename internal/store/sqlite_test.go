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

	"facets.barnlab.dev/internal/project"
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
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion+1)); err != nil {
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

func TestProjectRegistrySyncPreservesLocalMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "nested", "facets.db"))
	items := []project.Project{{
		ID:       "demo",
		Name:     "Demo",
		Metadata: map[string]any{"remote": "v1"},
	}}
	if err := store.SyncProjects(ctx, "kata", items); err != nil {
		t.Fatalf("SyncProjects(first) error = %v", err)
	}
	first, err := store.RegisteredProject(ctx, " kata ", " demo ")
	if err != nil {
		t.Fatalf("RegisteredProject(first) error = %v", err)
	}
	if first.FirstSeen.IsZero() || first.LastSeen.IsZero() || first.Metadata["remote"] != "v1" {
		t.Fatalf("first registry record = %#v", first)
	}
	updated, err := store.SetProjectMetadata(ctx, " kata ", " demo ", " directory ", "/tmp/demo")
	if err != nil {
		t.Fatalf("SetProjectMetadata() error = %v", err)
	}
	if updated.Metadata["directory"] != "/tmp/demo" {
		t.Fatalf("updated metadata = %#v", updated.Metadata)
	}
	if err := store.SyncProjects(ctx, "kata", []project.Project{{ID: "demo", Name: "Renamed", Metadata: map[string]any{"remote": "v2"}}}); err != nil {
		t.Fatalf("SyncProjects(second) error = %v", err)
	}
	final, err := store.RegisteredProject(ctx, "kata", "demo")
	if err != nil {
		t.Fatalf("RegisteredProject(final) error = %v", err)
	}
	if final.Name != "Renamed" || final.FirstSeen != first.FirstSeen || final.Metadata["remote"] != "v2" || final.Metadata["directory"] != "/tmp/demo" {
		t.Fatalf("final registry record = %#v", final)
	}
	if final.LastSeen.Before(first.LastSeen) {
		t.Fatalf("last seen moved backwards: first=%v final=%v", first.LastSeen, final.LastSeen)
	}
}

func TestRegisteredProjectDirectory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata map[string]any
		want     string
		wantOK   bool
	}{
		{name: "missing", metadata: nil},
		{name: "non-string", metadata: map[string]any{"directory": 42}},
		{name: "blank", metadata: map[string]any{"directory": " \t\n"}},
		{name: "normalized", metadata: map[string]any{"directory": " /work/facets "}, want: "/work/facets", wantOK: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory, ok := (RegisteredProject{Metadata: test.metadata}).Directory()
			if directory != test.want || ok != test.wantOK {
				t.Fatalf("Directory() = %q, %v; want %q, %v", directory, ok, test.want, test.wantOK)
			}
		})
	}
}

func TestProjectRegistryIdentityValidation(t *testing.T) {
	t.Parallel()

	store := &Store{}
	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{"registered source before ID", func() error { _, err := store.RegisteredProject(context.Background(), " ", " "); return err }, "store: project source is required"},
		{"registered ID", func() error { _, err := store.RegisteredProject(context.Background(), "kata", " "); return err }, "store: project ID is required"},
		{"metadata identity before key", func() error {
			_, err := store.SetProjectMetadata(context.Background(), " ", " ", " ", "value")
			return err
		}, "store: project source is required"},
		{"metadata key", func() error {
			_, err := store.SetProjectMetadata(context.Background(), "kata", "demo", " ", "value")
			return err
		}, "store: project metadata key is required"},
		{"disabled ID", func() error { _, err := store.SetProjectDisabled(context.Background(), "kata", " ", true); return err }, "store: project ID is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); err == nil || err.Error() != test.want {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestProjectDisabledStatePersistsAcrossSync(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "facets.db"))
	items := []project.Project{{ID: "demo", Name: "Demo", Metadata: map[string]any{"remote": "v1"}}}
	if err := store.SyncProjects(ctx, "kata", items); err != nil {
		t.Fatalf("SyncProjects() error = %v", err)
	}

	disabled, err := store.SetProjectDisabled(ctx, " kata ", " demo ", true)
	if err != nil {
		t.Fatalf("SetProjectDisabled(true) error = %v", err)
	}
	if disabled.DisabledAt == nil || disabled.DisabledAt.IsZero() {
		t.Fatalf("disabled record = %#v", disabled)
	}
	disabledAt := *disabled.DisabledAt

	if err := store.SyncProjects(ctx, "kata", []project.Project{{ID: "demo", Name: "Renamed", Metadata: map[string]any{"remote": "v2"}}}); err != nil {
		t.Fatalf("SyncProjects(second) error = %v", err)
	}
	current, err := store.RegisteredProject(ctx, "kata", "demo")
	if err != nil {
		t.Fatalf("RegisteredProject() error = %v", err)
	}
	if current.Name != "Renamed" || current.DisabledAt == nil || *current.DisabledAt != disabledAt {
		t.Fatalf("synced disabled record = %#v", current)
	}

	enabled, err := store.SetProjectDisabled(ctx, "kata", "demo", false)
	if err != nil {
		t.Fatalf("SetProjectDisabled(false) error = %v", err)
	}
	if enabled.DisabledAt != nil {
		t.Fatalf("enabled record = %#v", enabled)
	}
}

func TestDayFocusPersistsAndResolvesByLocalDay(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "facets.db")
	store := openTestStore(t, ctx, path)
	location := time.FixedZone("user", -4*60*60)
	previousDay := time.Date(2026, 8, 15, 23, 59, 0, 0, location)
	first := time.Date(2026, 8, 16, 9, 0, 0, 0, location)
	second := time.Date(2026, 8, 16, 17, 0, 0, 0, location)

	if _, err := store.CreateDayFocus(ctx, "  Finish the release  ", previousDay); err != nil {
		t.Fatalf("CreateDayFocus(previous) error = %v", err)
	}
	created, err := store.CreateDayFocus(ctx, "Finish the release", first)
	if err != nil {
		t.Fatalf("CreateDayFocus(first) error = %v", err)
	}
	if created.Focus != "Finish the release" || !created.CreatedAt.Equal(first.UTC()) || !created.DayStart.Equal(time.Date(2026, 8, 16, 4, 0, 0, 0, time.UTC)) {
		t.Fatalf("CreateDayFocus(first) = %#v", created)
	}
	if _, err := store.CreateDayFocus(ctx, "Ship the release", second); err != nil {
		t.Fatalf("CreateDayFocus(second) error = %v", err)
	}

	current, err := store.CurrentDayFocus(ctx, time.Date(2026, 8, 16, 23, 0, 0, 0, location))
	if err != nil {
		t.Fatalf("CurrentDayFocus(current day) error = %v", err)
	}
	if current.Focus != "Ship the release" || current.ID == created.ID {
		t.Fatalf("CurrentDayFocus(current day) = %#v", current)
	}
	if _, err := store.CurrentDayFocus(ctx, time.Date(2026, 8, 17, 0, 1, 0, 0, location)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CurrentDayFocus(next day) error = %v, want ErrNotFound", err)
	}

	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM day_focus").Scan(&count); err != nil {
		t.Fatalf("count day_focus rows error = %v", err)
	}
	if count != 3 {
		t.Fatalf("day_focus row count = %d, want 3", count)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	store = openTestStore(t, ctx, path)
	persisted, err := store.CurrentDayFocus(ctx, second)
	if err != nil || persisted.Focus != "Ship the release" {
		t.Fatalf("CurrentDayFocus(reopened) = %#v, %v", persisted, err)
	}
}

func TestDayFocusValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx, filepath.Join(t.TempDir(), "facets.db"))
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	if _, err := store.CreateDayFocus(ctx, " ", now); err == nil {
		t.Fatal("CreateDayFocus(blank) error = nil")
	}
	if _, err := store.CreateDayFocus(ctx, "focus", time.Time{}); err == nil {
		t.Fatal("CreateDayFocus(zero time) error = nil")
	}
	if _, err := store.CurrentDayFocus(ctx, time.Time{}); err == nil {
		t.Fatal("CurrentDayFocus(zero time) error = nil")
	}
}

func TestSavedViewLifecyclePersists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "facets.db")
	store := openTestStore(t, ctx, path)
	view := project.SavedView{
		ID: "view-1", Name: "Mine",
		Query: project.TaskQuery{Statuses: []project.Status{project.StatusOpen}, Assignees: []string{"bruce"}, Priorities: []int{1, 3}},
		Order: project.TaskOrder{Field: project.TaskOrderPriority, Direction: project.TaskOrderAscending},
	}
	created, err := store.CreateSavedView(ctx, view)
	if err != nil {
		t.Fatalf("CreateSavedView() error = %v", err)
	}
	if created.ID != view.ID || created.Name != view.Name || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("CreateSavedView() = %#v", created)
	}
	if _, err := store.CreateSavedView(ctx, project.SavedView{ID: "view-2", Name: "mine"}); !errors.Is(err, project.ErrConflict) {
		t.Fatalf("CreateSavedView(duplicate name) error = %v, want ErrConflict", err)
	}

	name := "Assigned"
	query := project.TaskQuery{Assignees: []string{"sam"}}
	updated, err := store.UpdateSavedView(ctx, created.ID, project.SavedViewPatch{Name: &name, Query: &query})
	if err != nil {
		t.Fatalf("UpdateSavedView() error = %v", err)
	}
	if updated.Name != name || len(updated.Query.Assignees) != 1 || updated.Query.Assignees[0] != "sam" || updated.Order != created.Order {
		t.Fatalf("UpdateSavedView() = %#v", updated)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	store = openTestStore(t, ctx, path)
	views, err := store.ListSavedViews(ctx)
	if err != nil {
		t.Fatalf("ListSavedViews() error = %v", err)
	}
	if len(views) != 1 || views[0].ID != created.ID || views[0].Name != name {
		t.Fatalf("ListSavedViews() = %#v", views)
	}
	if err := store.DeleteSavedView(ctx, created.ID); err != nil {
		t.Fatalf("DeleteSavedView() error = %v", err)
	}
	if err := store.DeleteSavedView(ctx, created.ID); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("DeleteSavedView(missing) error = %v, want project.ErrNotFound", err)
	}
}

func TestVersionTwoMigrationPreservesProjectRegistry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "facets.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	_, err = db.ExecContext(ctx, `
		CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		) STRICT;
		CREATE TABLE project_registry (
			source TEXT NOT NULL,
			project_id TEXT NOT NULL,
			name TEXT NOT NULL,
			metadata TEXT NOT NULL DEFAULT '{}',
			first_seen INTEGER NOT NULL,
			last_seen INTEGER NOT NULL,
			PRIMARY KEY (source, project_id)
		) STRICT;
		INSERT INTO project_registry VALUES ('kata', 'facets', 'Facets', '{"directory":"/work/facets"}', 1, 2);
		PRAGMA user_version = 2;
	`)
	if err != nil {
		t.Fatalf("seed version 2 database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed database: %v", err)
	}

	store := openTestStore(t, ctx, path)
	registered, err := store.RegisteredProject(ctx, "kata", "facets")
	if err != nil {
		t.Fatalf("RegisteredProject() error = %v", err)
	}
	if registered.Metadata["directory"] != "/work/facets" || registered.Name != "Facets" {
		t.Fatalf("migrated registry record = %#v", registered)
	}
	if views, err := store.ListSavedViews(ctx); err != nil || len(views) != 0 {
		t.Fatalf("ListSavedViews() = %#v, %v", views, err)
	}
	if _, err := store.CurrentDayFocus(ctx, time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CurrentDayFocus() after legacy migration error = %v, want ErrNotFound", err)
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
