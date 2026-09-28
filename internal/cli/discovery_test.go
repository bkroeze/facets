package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"facets.barnlab.dev/internal/project"
	"facets.barnlab.dev/internal/store"
)

func TestAppDiscoversMappedProjectDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	mappedRoot := filepath.Join(root, "mapped")
	nestedRoot := filepath.Join(mappedRoot, "nested")
	for _, directory := range []string{
		filepath.Join(mappedRoot, "child"),
		filepath.Join(nestedRoot, "child"),
		filepath.Join(root, "mapped-sibling"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, ".jj"), 0o755); err != nil {
		t.Fatal(err)
	}

	registry, err := store.Open(ctx, filepath.Join(root, "state", "facets.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() {
		if err := registry.Close(); err != nil {
			t.Errorf("registry.Close() error = %v", err)
		}
	}()
	if err := registry.SyncProjects(ctx, "kata", []project.Project{
		{ID: "parent", Name: "Parent", Metadata: map[string]any{"directory": mappedRoot}},
		{ID: "nested", Name: "Nested", Metadata: map[string]any{"directory": nestedRoot}},
	}); err != nil {
		t.Fatalf("SyncProjects() error = %v", err)
	}
	if err := registry.SyncProjects(ctx, "other", []project.Project{
		{ID: "wrong-provider", Name: "Wrong Provider", Metadata: map[string]any{"directory": filepath.Join(nestedRoot, "child")}},
	}); err != nil {
		t.Fatalf("SyncProjects(other) error = %v", err)
	}

	provider := &fakeProvider{}
	var stdout, stderr bytes.Buffer
	app := App{Provider: provider, ProjectStore: registry, Stdout: &stdout, Stderr: &stderr, Env: map[string]string{}}
	for _, test := range []struct {
		name string
		cwd  string
		want string
	}{
		{name: "exact mapped root", cwd: mappedRoot, want: "parent"},
		{name: "mapped descendant", cwd: filepath.Join(mappedRoot, "child"), want: "parent"},
		{name: "sibling prefix does not match", cwd: filepath.Join(root, "mapped-sibling"), want: filepath.Base(root)},
		{name: "most specific nested mapping", cwd: filepath.Join(nestedRoot, "child"), want: "nested"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stdout.Reset()
			stderr.Reset()
			provider.listProjectID = ""
			app.Cwd = test.cwd
			if code := app.Run(ctx, []string{"tasks"}); code != 0 {
				t.Fatalf("code = %d, stdout = %s, stderr = %s", code, stdout.String(), stderr.String())
			}
			if provider.listProjectID != test.want {
				t.Fatalf("project = %q, want %q", provider.listProjectID, test.want)
			}
		})
	}
}

func TestProjectDiscoveryPrecedenceAroundMappedDirectories(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "work", "child")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".jj"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, ".kata.toml")
	if err := os.WriteFile(configPath, []byte("[project]\nname = \"configured\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mappedCalls := 0
	mapped := func(string) (string, bool, error) {
		mappedCalls++
		return "mapped", true, nil
	}
	getenv := func(key string) string {
		if key == "FACETS_PROJECT" {
			return "environment"
		}
		return ""
	}
	for _, test := range []struct {
		name     string
		explicit string
		getenv   func(string) string
		want     string
	}{
		{name: "explicit flag", explicit: "explicit", getenv: getenv, want: "explicit"},
		{name: "environment", getenv: getenv, want: "environment"},
		{name: "workspace config", want: "configured"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := mappedCalls
			got, err := discoverProject(cwd, test.explicit, test.getenv, mapped)
			if err != nil {
				t.Fatalf("discoverProject() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("discoverProject() = %q, want %q", got, test.want)
			}
			if mappedCalls != before {
				t.Fatal("mapped lookup ran before a higher-precedence source")
			}
		})
	}

	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	got, err := discoverProject(cwd, "", nil, mapped)
	if err != nil {
		t.Fatalf("mapped discoverProject() error = %v", err)
	}
	if got != "mapped" {
		t.Fatalf("mapped discoverProject() = %q", got)
	}

	got, err = discoverProject(cwd, "", nil, func(string) (string, bool, error) {
		return "", false, nil
	})
	if err != nil {
		t.Fatalf("fallback discoverProject() error = %v", err)
	}
	if got != filepath.Base(root) {
		t.Fatalf("Jujutsu fallback = %q, want %q", got, filepath.Base(root))
	}
}

func TestMappedRegistryLookupFailureIsStructured(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".jj"), 0o755); err != nil {
		t.Fatal(err)
	}
	registry, err := store.Open(ctx, filepath.Join(root, "facets.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	if err := registry.Close(); err != nil {
		t.Fatalf("Store.Close() error = %v", err)
	}

	provider := &fakeProvider{}
	var stdout, stderr bytes.Buffer
	app := App{Provider: provider, ProjectStore: registry, Stdout: &stdout, Stderr: &stderr, Cwd: root, Env: map[string]string{}}
	if code := app.Run(ctx, []string{"--json", "tasks"}); code != 1 {
		t.Fatalf("code = %d, stdout = %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"type":"operational"`) || !strings.Contains(stdout.String(), "could not inspect the current workspace") {
		t.Fatalf("unstructured registry error: %s", stdout.String())
	}
	if provider.listProjectID != "" {
		t.Fatalf("registry failure silently fell back to project %q", provider.listProjectID)
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
