package agentdaemon

const skillUploadInstruction = `## Uploading Skills to Parsar

When asked to upload a generated Skill, use: parsar plugin add --json /absolute/path/to/bundle
The default Parsar sandbox includes this CLI. If it is unavailable on a custom
runtime, report the missing CLI prerequisite; do not ask for account credentials.
The directory must contain manifest.json with name, version, and skills (an array
of Markdown file paths relative to that directory). Example:
{"name":"team-guide","version":"1.0.0","skills":["skills/team-guide.md"]}
This uploads inline Skill instructions to this run's workspace. It does not
upload supporting files, execute plugin code, publish publicly, or bind an Agent.
The requesting user must be a workspace owner/admin. The upload credential is
provided through PARSAR_CAPABILITY_UPLOAD_TOKEN and expires after this run or one
hour. Never print credentials or ask the user to paste them. Other Parsar CLI
commands have separate authorization requirements.`

func (c *Connector) applySkillUpload(opts map[string]any, runID string) error {
	if c.skillUploadToken == nil {
		return nil
	}
	token, err := c.skillUploadToken(runID)
	if err != nil {
		return err
	}
	env := copyStringAnyMap(opts["env"])
	env["PARSAR_CAPABILITY_UPLOAD_TOKEN"] = token
	opts["env"] = env
	key := "system_prompt"
	if stringFromMap(opts, "override_system_prompt") != "" {
		key = "override_system_prompt"
	}
	base := stringFromMap(opts, key)
	if base != "" {
		base += "\n\n"
	}
	opts[key] = base + skillUploadInstruction
	return nil
}
