package localworkspace

import (
	"encoding/json"
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
	SystemPackageDirectory  = PackageDirectory + "/system"
	SystemPackageReceipt    = InitializationDirectory + "/system-root.json"
	SystemToolLauncher      = "/usr/local/bin/agents-api-tool-root"
)

// VerifyToolEnvironment is required only for execution consuming initialized
// tool configuration. It never makes Files reads depend on execution setup.
func VerifyToolEnvironment(systemPackages bool) error {
	paths := []string{InitializationDirectory, PackageDirectory, ToolEnvironmentShell, ToolEnvironmentJSON}
	if systemPackages {
		paths = append(paths, SystemPackageDirectory, SystemPackageReceipt, SystemToolLauncher)
	}
	for _, path := range paths {
		actual, err := filepath.EvalSymlinks(path)
		info, statErr := os.Lstat(path)
		if err != nil || statErr != nil || actual != path {
			return errors.New("initialized tool configuration unavailable")
		}
		if path == InitializationDirectory || path == PackageDirectory || path == SystemPackageDirectory {
			if !info.IsDir() {
				return errors.New("initialized tool directory unavailable")
			}
		} else if !info.Mode().IsRegular() || info.Mode().Perm()&0222 != 0 || info.Size() > 1024*1024 {
			return errors.New("initialized tool configuration is not immutable")
		}
	}
	if systemPackages {
		raw, err := os.ReadFile(SystemPackageReceipt)
		var receipt struct {
			Version int `json:"version"`
		}
		if err != nil || json.Unmarshal(raw, &receipt) != nil || receipt.Version != 1 {
			return errors.New("installed system tools unavailable")
		}
	}
	return nil
}
