package cli

import intentload "github.com/wloophq/wloop/internal/intent"

// wireWorkflows warns of each config drafts approval rule that never
// releases the comment it reads, of each one that does not load, and of
// each deprecated key the profile's manifests still set.
func (e *Env) wireWorkflows(w *serveWiring) {
	if w.Log == nil {
		return
	}
	for _, msg := range w.Cfg.DraftApprovalWarnings() {
		w.Log("config: %s", msg)
	}
	for _, err := range configApprovalProblems(w.Cfg, e.loadedIntents().Workflows) {
		w.Log("config: %v", err)
	}
	for _, p := range e.loadedIntents().Warnings.Sorted() {
		w.Log("profile: %s", intentload.Problems{p}.Error())
	}
}
