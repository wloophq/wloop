package cli

// turnStartDeps are what turn.start reads on units outside the round
// providers: the headless runner and the count of automated turns.
type turnStartDeps struct {
	Headless headlessStarter
	Turns    automatedTurns
}

// wireTurnStart wires turn.start on units other than pull requests and its daily budget.
func (e *Env) wireTurnStart(w *serveWiring) {
	if w.Deps != nil && w.Deps.Headless != nil {
		w.Engine.TurnStart.Headless = w.Deps.Headless
	}
	if w.DB != nil {
		w.Engine.TurnStart.Turns = w.DB
	}
}
