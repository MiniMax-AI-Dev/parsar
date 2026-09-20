package localworkspace

import (
	"errors"
	"os"
	"path/filepath"
)

// These paths belong to the packaged Runtime, not a harness or public template.
const (
	InitializationDirectory = "/environment/initialization"
	ToolEnvironmentShell    = InitializationDirectory + "/tool-env.sh"
	ToolEnvironmentJSON     = InitializationDirectory + "/tool-env.json"
	PackageDirectory        = "/environment/packages"
)

// VerifyToolEnvironment is required only for execution consuming initialized
// tool configuration. It never makes Files reads depend on execution setup.
func VerifyToolEnvironment() error {
	for _, path := range []string{InitializationDirectory, PackageDirectory, ToolEnvironmentShell, ToolEnvironmentJSON} {
		actual, err := filepath.EvalSymlinks(path)
		info, statErr := os.Lstat(path)
		if err != nil || statErr != nil || actual != path {
			return errors.New("initialized tool configuration unavailable")
		}
		if path == InitializationDirectory || path == PackageDirectory {
			if !info.IsDir() {
				return errors.New("initialized tool directory unavailable")
			}
		} else if !info.Mode().IsRegular() || info.Mode().Perm()&0222 != 0 || info.Size() > 1024*1024 {
			return errors.New("initialized tool configuration is not immutable")
		}
	}
	return nil
}
