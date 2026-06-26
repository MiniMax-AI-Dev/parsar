// Command parsar-daemon is the reverse-WebSocket worker that pairs a user
// machine with a Parsar server and exposes a local agent CLI
// subprocess as a connector_type=agent_daemon target. See
// apps/parsar-daemon/README.md for the subcommand spec.
package main

import (
	"fmt"
	"os"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/cli"
)

func main() {
	if err := cli.Execute(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "parsar-daemon: %v\n", err)
		os.Exit(1)
	}
}
