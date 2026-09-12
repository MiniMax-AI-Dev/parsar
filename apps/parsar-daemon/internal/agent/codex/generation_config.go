package codex

func extraConfigFromOpts(opts map[string]any) [][2]string {
	var out [][2]string
	if rs := stringOpt(opts, "reasoning_summary"); rs != "" {
		// codex app-server has no per-call flag; route via -c override.
		// TOML literal — quoted string keeps shell-safe special chars.
		out = append(out, [2]string{"model_reasoning_summary", strconv(rs)})
	}
	if mode := stringOpt(opts, "web_search"); mode != "" {
		out = append(out, [2]string{"web_search", strconv(mode)})
	}
	if verbosity := stringOpt(opts, "model_verbosity"); verbosity != "" {
		out = append(out, [2]string{"model_verbosity", strconv(verbosity)})
	}
	return out
}
