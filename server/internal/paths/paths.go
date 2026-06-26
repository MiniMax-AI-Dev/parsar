package paths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const AppDirName = ".parsar"

func HomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("cannot resolve user home directory")
	}
	return home, nil
}

func Root() (string, error) {
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, AppDirName), nil
}

func MustBeUserPath(input string) (string, error) {
	if input == "" {
		return "", errors.New("path is required")
	}
	if strings.HasPrefix(input, "~/") {
		home, err := HomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(input, "~/")), nil
	}
	if filepath.IsAbs(input) {
		return filepath.Clean(input), nil
	}
	return "", errors.New("path must be absolute or start with ~/; relative paths are not accepted")
}
