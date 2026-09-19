package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/binpath"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/mcode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

func discoverMCodeWorkspace(rc *runContext, discovery *agentCLIDiscovery) {
	mode := os.Getenv("PARSAR_MCODE_WORKSPACE")
	if mode == "" {
		return
	}
	fail := func(err error) {
		discovery.MCode.Available = false
		fmt.Fprintf(rc.stderr, "parsar-daemon: mcode workspace unavailable: %v\n", err)
	}
	if mode != "managed" || !discovery.MCode.Available || !mcode.SupportsExecution(discovery.MCode.Version) {
		fail(fmt.Errorf("managed execution requires the qualified native version and opt-in"))
		return
	}
	binding, err := localworkspace.Load()
	if err != nil || binding == nil {
		fail(fmt.Errorf("dedicated local Runtime binding required"))
		return
	}
	root, err := paths.Root()
	if err != nil {
		fail(err)
		return
	}
	node := os.Getenv("PARSAR_MCODE_NODE")
	if node == "" {
		node = "node"
	}
	node, err = exec.LookPath(node)
	if err != nil {
		fail(err)
		return
	}
	node, err = filepath.Abs(node)
	if err != nil {
		fail(err)
		return
	}
	binary, err := exec.LookPath(binpath.MCode())
	if err != nil {
		fail(err)
		return
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		fail(err)
		return
	}
	c, err := mcode.ConfigureLocal(binary, node, os.Getenv("PARSAR_MCODE_WORKSPACE_BRIDGE"), root, os.Getenv("PARSAR_RUNTIME_WORKSPACE"), binding.NetworkAccess(), os.Getenv("PARSAR_RUNTIME_STAGING"))
	if err == nil {
		err = mcode.CheckWorkspace(context.Background(), c)
	}
	if err != nil {
		fail(err)
		return
	}
	discovery.MCodeWorkspace = &c
	caps := &discovery.MCode.Capabilities
	caps.EnvironmentNone = false
	caps.Preparation, caps.LocalEnvironment, caps.LocalEnvironmentNetworkPolicy = true, true, true
	caps.WorkspaceReadPreparation = true
}
