package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

func discoverProject(cwd, explicit string, getenv func(string) string) (string, error) {
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
