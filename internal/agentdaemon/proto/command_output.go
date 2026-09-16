package proto

// TypeCommandOutput carries opt-in incremental output for an observed command.
const TypeCommandOutput = "command_output"

// CommandOutputPayload references an existing command observation in this run.
// Delta contains native text, not a complete or byte-exact process output stream.
type CommandOutputPayload struct {
	ID    string `json:"id"`
	Delta string `json:"delta"`
}
