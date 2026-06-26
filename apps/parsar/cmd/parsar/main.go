// Command parsar is the in-sandbox Parsar companion CLI. Identity comes
// from the server-side runtime bound to PARSAR_RUNNER_TOKEN; the CLI never
// accepts identity as flags so a leaked token can't write to the wrong
// scope.
package main

import (
	"fmt"
	"os"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar/internal/cli"
)

func main() {
	if err := cli.Execute(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "parsar: %v\n", err)
		os.Exit(1)
	}
}
