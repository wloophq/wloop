package cli

// wireWorkflows warns of each proposals.<rule>.approval the config still
// sets for a rule a default workflow replaced; draft.propose applies it.
func (e *Env) wireWorkflows(w *serveWiring) {
	if w.Log == nil {
		return
	}
	for _, msg := range w.Cfg.Proposals.LegacyWarnings() {
		w.Log("config: %s", msg)
	}
}
