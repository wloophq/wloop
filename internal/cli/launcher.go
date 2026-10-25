package cli

import (
	"cmp"
	"context"
	"fmt"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/instructions"
	"github.com/wloophq/wloop/internal/core/turns"
	dchange "github.com/wloophq/wloop/internal/domain/change"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dpr "github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
)

// cliLauncher runs the turns the core asks for through the round code.
type cliLauncher struct{ e *Env }

var _ turns.Launcher = cliLauncher{}

// Launch sends a resume of turn l.ResumeOf, or a new turn when it is 0, in
// the workspace the intent's manifest names, created again when it is gone;
// a probe goes to a new session of the workspace it names, or of the one its
// resumed turn was sent to.
func (c cliLauncher) Launch(ctx context.Context, l turns.Launch) error {
	e := c.e
	d, err := e.intentDef(string(l.Intent))
	if err != nil {
		return err
	}
	if d.Launch == builtin.LaunchProbe {
		return c.probe(ctx, d, l)
	}
	if l.Unit.Provider != unit.GitHub {
		return e.launchUnit(ctx, d, l)
	}
	run, ok := workspaceRuns()[d.Workspace]
	if !ok {
		return fmt.Errorf(brand.Name+" cannot launch a %s turn: no launch names its %s workspace", d.Name, d.Workspace)
	}
	ref, err := githubRef(l.Unit)
	if err != nil {
		return err
	}
	base, err := e.loadConfig()
	if err != nil {
		return err
	}
	home, err := e.roundAgentHome(ctx, ref)
	if err != nil {
		return err
	}
	cfg := prConfig(base, ref, home)
	pr, err := e.launchPR(ctx, ref)
	if err != nil {
		return err
	}
	r := turn.Request{Intent: turn.Intent(d.Name), Trigger: requestTrigger(l.Trigger), Unit: ref.Unit(), Payload: turnPR(pr), Guidance: l.Guidance, Inputs: l.Inputs, Why: l.Why}
	e.withAddressReason(ctx, cfg, d, &r)
	if l.ResumeOf != 0 {
		books, err := e.Turns.Store.Books(ctx, l.Unit)
		if err != nil {
			return err
		}
		b := books[l.Unit]
		b.Unit = l.Unit
		prev, ok := turnOf(b, l.ResumeOf)
		if !ok {
			return fmt.Errorf("%s has no turn %d to resume", ref, l.ResumeOf)
		}
		r.Trigger, r.Guidance = requestTrigger(prev.Trigger), cmp.Or(l.Guidance, prev.Guidance)
		if r.Inputs == nil {
			r.Inputs = prev.Inputs
		}
		r.Resume = instructions.ResumeOf(b, l.ResumeOf, l.Answer, pr.HeadRefOid, "")
		if r.History, err = e.turnHistory(ctx, l.Unit, b); err != nil {
			return err
		}
	}
	submitted, err := run(e, ctx, roundRun{def: d, cfg: cfg, r: r, pr: pr, resumeOf: l.ResumeOf})
	if err != nil {
		return err
	}
	sr := &sentRound{env: e, ctx: ctx, ref: ref, pr: pr, cfg: cfg, inputs: r.Inputs, submitted: submitted, guidance: r.Guidance}
	runPhase(routeLaunch, d, sr, sentPhase)
	if !writesRounds(routeLaunch, d, sr) {
		return nil
	}
	return e.updateRounds(ctx, func(st *round.Store) { runPhase(routeLaunch, d, sr, statePhase(st)) })
}

// reviewFingerprint is what the round just sent covers, so the sweep plans
// no follow-up of the same reviews; "" when it cannot be read.
func (e *Env) reviewFingerprint(ctx context.Context, cfg config.Config, ref github.Ref) string {
	full, viewer, err := e.coveredPR(ctx, ref)
	if err != nil {
		fmt.Fprintf(e.Err, "warning: %s: reading the reviews the round covers: %v\n", ref, err)
		return ""
	}
	return dchange.Reviewer(dchange.ReviewerInput{PR: full.Facts, Viewer: viewer, TrustedOwners: []string{ref.Owner}, TrustedAuthors: cfg.TrustedAuthors}).Fingerprint
}

// updateRounds applies a resume's effects to the round state.
func (e *Env) updateRounds(ctx context.Context, apply func(st *round.Store)) error {
	if e.Rounds == nil {
		return nil
	}
	st, err := e.Rounds.Load(ctx)
	if err != nil {
		return err
	}
	apply(st)
	return e.Rounds.Save(ctx, st)
}

// roundAgentHome is the agent home remembered for the pull request's rounds.
func (e *Env) roundAgentHome(ctx context.Context, ref github.Ref) (string, error) {
	if e.Rounds == nil {
		return "", nil
	}
	return e.Rounds.AgentHome(ctx, ref)
}

func requestTrigger(t dturn.Trigger) turn.Trigger {
	if t == dturn.Automated {
		return turn.Automated
	}
	return turn.ByHand
}

func turnOf(b dturn.Book, seq int64) (dturn.Turn, bool) {
	for _, t := range b.Turns {
		if t.Seq == seq {
			return t, true
		}
	}
	return dturn.Turn{}, false
}

// runInReview sends the round in the pull request's review workspace,
// created when it has none.
func (e *Env) runInReview(ctx context.Context, run roundRun) (bool, error) {
	ref, err := githubRef(run.r.Unit)
	if err != nil {
		return false, err
	}
	if err := e.roundReady(); err != nil {
		return false, err
	}
	if run.st != nil && run.rememberHome != "" {
		run.st.SetAgentHome(ref.String(), run.rememberHome)
	}
	sent, err := e.sendTurn(ctx, run.cfg, run.r)
	if err != nil {
		return false, err
	}
	sent.resume(run.resumeOf)
	submitted, err := e.openReview(ctx, run.cfg, ref, run.pr, sent, run.options(roundOptions{allowUserActive: true, pr: run.pr}))
	if run.resumeOf == 0 {
		e.recordGuidance(ctx, ref, run.r.Guidance)
	}
	return submitted, err
}

// runInAuthor sends the round in the pull request's author workspace. A
// resume creates it again when it is gone; a new round creates it only on a
// pull request that is the user's.
func (e *Env) runInAuthor(ctx context.Context, run roundRun) (bool, error) {
	cfg, r, pr := run.cfg, run.r, run.pr
	if err := e.roundReady(); err != nil {
		return false, err
	}
	var ws wsdomain.Workspace
	var action string
	var err error
	if run.resumeOf != 0 {
		var repos []wsdomain.Repo
		var all []wsdomain.Workspace
		if repos, err = e.WS.Repos(ctx); err != nil {
			return false, err
		}
		if all, err = e.WS.List(ctx); err != nil {
			return false, err
		}
		ws, action, err = e.authorWorkspace(ctx, cfg, prOf(r.Unit), pr, e.prOf(ctx, pr), repos, all)
	} else {
		ws, action, err = e.addressWorkspace(ctx, cfg, prOf(r.Unit), pr, e.prOf(ctx, pr))
	}
	if err != nil {
		return false, err
	}
	if action != "" && run.resumeOf == 0 {
		fmt.Fprintf(e.Out, "%s\n", action)
	}
	submitted, verb, _, err := e.startAddress(ctx, cfg, ws, r, run.options(roundOptions{allowUserActive: true, resumeOf: run.resumeOf, trusted: trusts(cfg, pr), pr: pr}))
	if err != nil {
		return submitted, fmt.Errorf("%s: %w", ws.DisplayName, err)
	}
	if !submitted {
		verb += " (submission not observed; check the session)"
	}
	if run.resumeOf != 0 {
		fmt.Fprintf(e.Out, "%s; triage in %s, %s: %s\n", action, ws.DisplayName, verb, pr.Title)
	} else {
		fmt.Fprintf(e.Out, "triage in %s, %s: %s\n%s\n", ws.DisplayName, verb, pr.Title, ws.Path)
	}
	return submitted, nil
}

// runInPRElseMain sends the round in a new session of the pull request's
// workspace, else of the repository's main checkout, or in the caller's own
// session.
func (e *Env) runInPRElseMain(ctx context.Context, run roundRun) (bool, error) {
	return true, e.nudge(ctx, run.cfg, run.r, run.pr, run.resumeOf)
}

// askedRound runs a round of d the user asked for through the daemon's
// action: home is the caller's agent home, kept for the pull request's next
// rounds when its project names none.
func (e *Env) askedRound(ctx context.Context, d dintent.Def, base config.Config, ref github.Ref, guidance string, inputs map[string]any, home string) error {
	run, ok := workspaceRuns()[d.Workspace]
	if !ok {
		return fmt.Errorf("%s runs from the command line or an agent session in a %s workspace, not from the daemon", d.Name, d.Workspace)
	}
	cfg := prConfig(base, ref, home)
	remember := ""
	if base.For(ref.Owner, ref.Repo).AgentHome == "" {
		remember = home
	}
	return e.handRound(ctx, routeAsked, ref, guidance, d, func(st *round.Store, sr *sentRound) (bool, error) {
		pr, err := e.openPR(ctx, ref)
		if err != nil {
			return false, err
		}
		r := turn.Request{Intent: turn.Intent(d.Name), Trigger: turn.ByHand, Unit: ref.Unit(), Payload: turnPR(pr), Guidance: sr.guidance, Inputs: inputs}
		e.withAddressReason(ctx, cfg, d, &r)
		submitted, err := run(e, ctx, roundRun{def: d, cfg: cfg, r: r, pr: pr, st: st, rememberHome: remember})
		sr.pr, sr.cfg, sr.inputs, sr.submitted = pr, cfg, inputs, submitted
		if err == nil {
			runPhase(routeAsked, d, sr, sentPhase)
		}
		return submitted, err
	})
}

func githubRef(u unit.Unit) (github.Ref, error) {
	if u.Provider != unit.GitHub {
		return github.Ref{}, fmt.Errorf("the %s unit %s is not a pull request", u.Provider, u.Key)
	}
	return dpr.ParseKey(u.Key)
}

func prOf(u unit.Unit) github.Ref {
	ref, _ := githubRef(u)
	return ref
}

func refsOf(units []unit.Unit) []github.Ref {
	refs := make([]github.Ref, len(units))
	for i, u := range units {
		refs[i] = prOf(u)
	}
	return refs
}
