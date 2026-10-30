package cli

import intentload "github.com/wloophq/wloop/internal/intent"

// wireWorkflows warns of each config gates approval rule that reads a
// draft's delivery and names none, of each config workflows.<name> block that
// does not load over its workflow, and of each deprecated key the profile's
// manifests still set.
func (e *Env) wireWorkflows(w *serveWiring) {
	if w.Log == nil {
		return
	}
	for _, msg := range w.Cfg.GateApprovalWarnings() {
		w.Log("config: %s", msg)
	}
	for _, err := range configWorkflowProblems(w.Cfg, e.loadedIntents()) {
		w.Log("config: %v", err)
	}
	for _, p := range e.loadedIntents().Warnings.Sorted() {
		w.Log("profile: %s", intentload.Problems{p}.Error())
	}
}
