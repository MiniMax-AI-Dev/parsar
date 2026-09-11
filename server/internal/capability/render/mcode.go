package render

// mcode shares the portable Skill, MCP and prompt payloads; ACP conversion belongs to the daemon.
type mcodeRenderer struct{ codexRenderer }

func (mcodeRenderer) Target() Target { return TargetMCode }
