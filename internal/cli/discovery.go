package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"facets.barnlab.dev/internal/store"
)

func discoverProject(cwd, explicit string, getenv func(string) string, mapped func(string) (string, bool, error)) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit), nil
	}
	if getenv != nil {
		if value := strings.TrimSpace(getenv("FACETS_PROJECT")); value != "" {
			return value, nil
		}
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}

	for directory := cwd; ; directory = filepath.Dir(directory) {
		name, present, configErr := kataProjectName(filepath.Join(directory, ".kata.toml"))
		if configErr != nil {
			return "", configErr
		}
		if present {
			return name, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
	}
	if mapped != nil {
		name, present, mappingErr := mapped(cwd)
		if mappingErr != nil {
			return "", mappingErr
		}
		if present {
			return name, nil
		}
	}
	for directory := cwd; ; directory = filepath.Dir(directory) {
		path := filepath.Join(directory, ".jj")
		info, statErr := os.Stat(path)
		switch {
		case statErr == nil:
			if !info.IsDir() {
				return "", fmt.Errorf("inspect %s: expected a directory", path)
			}
			name := filepath.Base(directory)
			if name == "." || name == string(filepath.Separator) || name == "" {
				return "", fmt.Errorf("inspect %s: repository directory has no project name", path)
			}
			return name, nil
		case !errors.Is(statErr, fs.ErrNotExist):
			return "", fmt.Errorf("inspect %s: %w", path, statErr)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
	}
	return "", errProjectNotDiscovered
}

func mappedProject(cwd string, projects []store.RegisteredProject) (string, bool, error) {
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return "", false, fmt.Errorf("resolve current directory: %w", err)
	}
	cwd = filepath.Clean(cwd)

	var (
		selectedID   string
		selectedRoot string
	)
	for _, registered := range projects {
		directory, ok := registered.Directory()
		if !ok || !filepath.IsAbs(directory) {
			continue
		}
		root := filepath.Clean(directory)
		relative, relErr := filepath.Rel(root, cwd)
		if relErr != nil {
			return "", false, fmt.Errorf("compare project directory %s with %s: %w", root, cwd, relErr)
		}
		if relative != "." && (relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
			continue
		}
		if len(root) < len(selectedRoot) {
			continue
		}
		if len(root) == len(selectedRoot) && selectedID != "" && registered.ID != selectedID {
			return "", false, fmt.Errorf("project directory %s is mapped to both %q and %q", root, selectedID, registered.ID)
		}
		selectedID = registered.ID
		selectedRoot = root
	}
	return selectedID, selectedID != "", nil
}

func kataProjectName(path string) (string, bool, error) {
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", true, fmt.Errorf("inspect %s: %w", path, err)
	}
	var config struct {
		Project struct {
			Name string `toml:"name"`
		} `toml:"project"`
	}
	if _, err := toml.DecodeFile(path, &config); err != nil {
		return "", true, fmt.Errorf("parse %s: %w", path, err)
	}
	name := strings.TrimSpace(config.Project.Name)
	if name == "" {
		return "", true, fmt.Errorf("parse %s: [project].name is required", path)
	}
	return name, true, nil
}
