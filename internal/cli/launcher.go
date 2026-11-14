package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/instructions"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dpr "github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// cliLauncher runs the turns the core asks for through the round code.
type cliLauncher struct{ e *Env }

var _ turns.Launcher = cliLauncher{}

// Launch sends l's turn, a new one or a resume of turn l.ResumeOf, in the
// workspace the intent's manifest names, created again when it is gone; a
// probe goes to a new session of the workspace it names, or of the one its
// resumed turn was sent to. A new turn's effects are its start's; a resumed
// turn on a pull request applies its plugin's turn effects and records the
// guidance it carries.
func (c cliLauncher) Launch(ctx context.Context, l turns.Launch) (turns.Outcome, error) {
	e := c.e
	l = admittedLaunch(l)
	d, err := e.intentDef(string(l.Intent))
	if err != nil {
		return turns.Outcome{}, err
	}
	if d.Builds != "" {
		return turns.Outcome{}, fmt.Errorf("the intent %s builds a %s and starts no turn", d.Name, d.Builds)
	}
	if d.Launch == builtin.LaunchProbe {
		return c.probe(ctx, d, l)
	}
	if _, ok := dpr.RefOf(l.Unit); !ok {
		return e.launchUnit(ctx, d, l)
	}
	run, ok := workspaceRuns()[d.Workspace]
	if !ok {
		return turns.Outcome{}, fmt.Errorf(brand.Name+" cannot launch a %s turn: no launch names its %s workspace", d.Name, d.Workspace)
	}
	ref, err := githubRef(l.Unit)
	if err != nil {
		return turns.Outcome{}, err
	}
	base, err := e.loadConfig()
	if err != nil {
		return turns.Outcome{}, err
	}
	home, err := e.roundAgentHome(ctx, ref)
	if err != nil {
		return turns.Outcome{}, err
	}
	cfg := prConfig(base, ref, home)
	pr, err := e.launchPR(ctx, ref)
	if err != nil {
		return turns.Outcome{}, err
	}
	r := turn.Request{Intent: turn.Intent(d.Name), Trigger: requestTrigger(l.Trigger), Unit: ref.Unit(), Payload: turnPR(pr), Guidance: l.Guidance, Inputs: l.Inputs, Why: l.Why}
	e.withAddressReason(ctx, cfg, d, &r)
	if l.ResumeOf != 0 {
		books, err := e.Turns.Store.Books(ctx, l.Unit)
		if err != nil {
			return turns.Outcome{}, err
		}
		b := books[l.Unit]
		b.Unit = l.Unit
		prev, ok := turnOf(b, l.ResumeOf)
		if !ok {
			return turns.Outcome{}, fmt.Errorf("%s has no turn %d to resume", ref, l.ResumeOf)
		}
		if r.Inputs == nil {
			r.Inputs = turns.InputsOf(prev)
		}
		r.Items, r.Task = turns.ItemsOf(prev), turns.TaskOf(prev)
		r.Resume = instructions.ResumeOf(b, l.ResumeOf, l.Answer, pr.HeadRefOid, "")
		if r.Resume != nil {
			r.Resume.Drafts = e.resumeDrafts(ctx, b, l.ResumeOf, pr.HeadRefOid)
		}
		if r.History, err = e.turnHistory(ctx, l.Unit, b, ""); err != nil {
			return turns.Outcome{}, err
		}
	}
	resume := l.Turn.Resumed()
	if resume {
		if err := e.guidesReady(d); err != nil {
			return turns.Outcome{}, err
		}
		if err := e.checkEffects(d, ref.Unit()); err != nil {
			return turns.Outcome{}, err
		}
	}
	id, submitted, err := run(e, ctx, roundRun{def: d, cfg: cfg, r: r, pr: pr, turn: l.Turn})
	if resume && id != "" {
		e.recordGuidance(ctx, ref, r.Guidance)
	}
	out := turns.Outcome{ID: id, Head: pr.HeadRefOid, Submitted: submitted}
	if err != nil {
		return out, err
	}
	if resume {
		e.applyPluginEffects(ctx, d, ref.Unit(), src.EffectRequest{Turn: &src.TurnRef{ID: id, Head: pr.HeadRefOid}, Route: src.RouteLaunch, Taken: submitted}, r.Inputs)
	}
	return out, nil
}

// admittedLaunch is l with the identity of the turn it carries.
func admittedLaunch(l turns.Launch) turns.Launch {
	t := l.Turn
	l.Unit, l.Intent, l.Guidance, l.Inputs, l.Trigger, l.ResumeOf = t.Unit(), t.Intent(), t.Guidance(), t.Inputs(), t.Trigger(), t.ResumeOf()
	return l
}

// roundAgentHome is the agent home remembered for the pull request's rounds.
func (e *Env) roundAgentHome(ctx context.Context, ref dpr.Ref) (string, error) {
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
func (e *Env) runInReview(ctx context.Context, run roundRun) (string, bool, error) {
	ref, err := githubRef(run.r.Unit)
	if err != nil {
		return "", false, err
	}
	if run.st != nil && run.rememberHome != "" {
		run.st.SetAgentHome(ref.String(), run.rememberHome)
	}
	sent, err := e.sendTurn(ctx, run.cfg, run.turn, run.r)
	if err != nil {
		return "", false, err
	}
	submitted, err := e.openReview(ctx, run.cfg, ref, run.pr, sent, run.options(roundOptions{allowUserActive: true, pr: run.pr}))
	return sent.ID, submitted, err
}

// runInAuthor sends the round in the pull request's author workspace. A
// resume creates it again when it is gone; a new round creates it only on a
// pull request that is the user's.
func (e *Env) runInAuthor(ctx context.Context, run roundRun) (string, bool, error) {
	cfg, r, pr := run.cfg, run.r, run.pr
	var ws wsdomain.Workspace
	var action string
	var err error
	resumeOf := run.turn.ResumeOf()
	if resumeOf != 0 {
		var repos []wsdomain.Repo
		var all []wsdomain.Workspace
		if repos, err = e.WS.Repos(ctx); err != nil {
			return "", false, err
		}
		if all, err = e.WS.List(ctx); err != nil {
			return "", false, err
		}
		ws, action, err = e.authorWorkspace(ctx, cfg, prOf(r.Unit), pr, e.prOf(ctx, pr), repos, all)
	} else {
		ws, action, err = e.addressWorkspace(ctx, cfg, prOf(r.Unit), pr, e.prOf(ctx, pr))
	}
	if err != nil {
		return "", false, err
	}
	if action != "" && resumeOf == 0 {
		fmt.Fprintf(e.Out, "%s\n", action)
	}
	submitted, verb, id, err := e.startAddress(ctx, cfg, ws, r, run.options(roundOptions{allowUserActive: true, turn: run.turn, trusted: trusts(cfg, pr), pr: pr}))
	if err != nil {
		return id, submitted, fmt.Errorf("%s: %w", ws.DisplayName, err)
	}
	if !submitted {
		verb += " (submission not observed; check the session)"
	}
	if resumeOf != 0 {
		fmt.Fprintf(e.Out, "%s; triage in %s, %s: %s\n", action, ws.DisplayName, verb, pr.Title)
	} else {
		fmt.Fprintf(e.Out, "triage in %s, %s: %s\n%s\n", ws.DisplayName, verb, pr.Title, ws.Path)
	}
	return id, submitted, nil
}

// runInPRElseMain sends the round in a new session of the pull request's
// workspace, else of the repository's main checkout, or in the caller's own
// session.
func (e *Env) runInPRElseMain(ctx context.Context, run roundRun) (string, bool, error) {
	return e.nudge(ctx, run.cfg, run.turn, run.r, run.pr)
}

// askedRound starts a round of d a person asked for through the daemon's
// action: home is the caller's agent home, kept for the pull request's next
// rounds when its project names none; reason is how they asked, "" for the
// action itself.
func (e *Env) askedRound(ctx context.Context, d dintent.Def, base config.Config, ref dpr.Ref, guidance string, inputs map[string]any, home, reason string) error {
	run, ok := workspaceRuns()[d.Workspace]
	if !ok {
		return fmt.Errorf("%s runs from the command line or an agent session in a %s workspace, not from the daemon", d.Name, d.Workspace)
	}
	if e.Turns == nil || e.Turns.Starter == nil {
		return errors.New("no turn starts here: the daemon runs a round asked for by hand")
	}
	remember := ""
	if base.For(ref.Owner, ref.Repo).AgentHome == "" {
		remember = home
	}
	_, err := e.Turns.Starter.Start(ctx, turns.Request{Unit: ref.Unit(), Intent: dturn.Intent(d.Name), Guidance: guidance, Inputs: inputs,
		Trigger: dturn.ByHand, Route: src.RouteAsked, ByPerson: true, Reason: reason},
		&askedRunner{e: e, d: d, cfg: prConfig(base, ref, home), ref: ref, run: run, remember: remember})
	return err
}

// askedRunner runs a round asked for through the daemon's action in the
// workspace its intent names: its guard reads the pull request, refusing
// one that is not open before any effect applies.
type askedRunner struct {
	e        *Env
	d        dintent.Def
	cfg      config.Config
	ref      dpr.Ref
	run      workspaceRun
	remember string

	pr *dpr.PullRequest
}

func (x *askedRunner) Guard(ctx context.Context, _ turns.Turn) error {
	var err error
	x.pr, err = x.e.openPR(ctx, x.ref)
	return err
}

func (x *askedRunner) KeptBack(string) { x.e.noteKeptBack(x.ref) }

func (x *askedRunner) Run(ctx context.Context, t turns.Turn) (turns.Outcome, error) {
	e := x.e
	r := turn.Request{Intent: turn.Intent(x.d.Name), Trigger: turn.ByHand, Unit: x.ref.Unit(), Payload: turnPR(x.pr), Guidance: t.Guidance(), Inputs: t.Inputs()}
	e.withAddressReason(ctx, x.cfg, x.d, &r)
	rr := roundRun{def: x.d, cfg: x.cfg, r: r, pr: x.pr, turn: t, rememberHome: x.remember}
	if x.remember == "" {
		id, submitted, err := x.run(e, ctx, rr)
		return turns.Outcome{ID: id, Head: x.pr.HeadRefOid, Submitted: submitted}, err
	}
	rounds, err := e.rounds()
	if err != nil {
		return turns.Outcome{}, err
	}
	if rr.st, err = rounds.Load(ctx); err != nil {
		return turns.Outcome{}, err
	}
	id, submitted, err := x.run(e, ctx, rr)
	out := turns.Outcome{ID: id, Head: x.pr.HeadRefOid, Submitted: submitted}
	if err != nil {
		return out, err
	}
	return out, rounds.Save(ctx, rr.st)
}

func githubRef(u unit.Unit) (dpr.Ref, error) {
	if u.Provider != (dpr.Codec{}).Provider() {
		return dpr.Ref{}, fmt.Errorf("the %s unit %s is not a pull request", u.Provider, u.Key)
	}
	return dpr.ParseKey(u.Key)
}

// isPR says whether u names a pull request.
func isPR(u unit.Unit) bool {
	_, ok := dpr.RefOf(u)
	return ok
}

func prOf(u unit.Unit) dpr.Ref {
	ref, _ := githubRef(u)
	return ref
}

func refsOf(units []unit.Unit) []dpr.Ref {
	refs := make([]dpr.Ref, len(units))
	for i, u := range units {
		refs[i] = prOf(u)
	}
	return refs
}
