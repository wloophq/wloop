package cli

import intentload "github.com/wloophq/wloop/internal/intent"

// wireWorkflows warns of each proposals.<rule>.approval the config still
// sets for a rule a default workflow replaced, which draft.propose applies,
// and of each deprecated key the profile's manifests still set.
func (e *Env) wireWorkflows(w *serveWiring) {
	if w.Log == nil {
		return
	}
	for _, msg := range append(w.Cfg.Proposals.LegacyWarnings(), w.Cfg.DraftApprovalWarnings()...) {
		w.Log("config: %s", msg)
	}
	for _, err := range configApprovalProblems(w.Cfg, e.loadedIntents().Workflows) {
		w.Log("config: %v", err)
	}
	for _, p := range e.loadedIntents().Warnings.Sorted() {
		w.Log("profile: %s", intentload.Problems{p}.Error())
	}
}
