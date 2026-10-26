package cli

// wireBriefs registers each plugin's BriefSource with the brief queue, which
// briefs an on-demand provider's units through it.
func (e *Env) wireBriefs(w *serveWiring) {
	q := w.Engine.Briefs
	if q == nil {
		return
	}
	for _, m := range w.Modules {
		if m.Briefs != nil {
			q.Register(m.ID, m.Briefs)
		}
	}
}
