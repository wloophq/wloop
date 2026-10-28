package cli

// wireBriefs registers every module's BriefSource with the brief queue,
// which hears of each brief a plugin writes through the broker's PutBrief.
func (e *Env) wireBriefs(w *serveWiring) {
	q := w.Engine.Briefs
	if q == nil {
		return
	}
	if e.briefsHeard != nil {
		e.briefsHeard.Store(q)
	}
	for _, m := range w.Modules {
		if m.Briefs != nil {
			q.Register(m.ID, m.Briefs)
		}
	}
}
