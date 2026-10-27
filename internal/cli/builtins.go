package cli

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
)

// nudgedWho is the input nudge.prepare fills with whom the round asks,
// which pr.nudged records.
const nudgedWho = "who"

// The workspace builtins whose role links a worktree to the pull request.
const (
	workspaceReview = builtin.WorkspaceReview
	workspaceAuthor = builtin.WorkspaceAuthor
)

// beforeTakeOver is the before builtin that takes the pull request over.
const beforeTakeOver = "take_over"

// contextPR is the context of an intent whose turns are about a real pull
// request.
const contextPR = builtin.ContextPR

// route is a way a round of an intent goes out from the command line.
type route int

const (
	// routeSession is a round asked for from an agent session (byHand).
	routeSession route = iota
	// routeAsked is a round asked for through the daemon's action (askedRound).
	routeAsked
	// routeLaunch is a resume or a launch the turn service asks for.
	routeLaunch
)

// sentRound is what a turn effect reads of a round on its way out; the
// route fills it as the round goes.
type sentRound struct {
	env       *Env
	ctx       context.Context
	ref       github.Ref
	pr        *github.PullRequest
	cfg       config.Config
	inputs    map[string]any
	id        string
	submitted bool
	guidance  string
	// fingerprint is the reviews the round covers, "" when unread.
	fingerprint string
}

// effectRun is what an effect does on a route, by phase: guide before the
// round, sent once it went out, state on the round state the route loaded,
// unsent when it did not go out.
type effectRun struct {
	guide, sent, unsent func(r *sentRound)
	state               func(r *sentRound, st *round.Store)
	// writesIf, when set, says whether the state phase writes for r.
	writesIf func(r *sentRound) bool
}

// onSentEffect is a turn effect and what it does on each route.
type onSentEffect struct {
	name string
	on   map[route]effectRun
}

var (
	keepsGuidance = effectRun{
		guide:  func(r *sentRound) { r.guidance = r.env.handGuidance(r.ctx, r.ref, r.guidance) },
		unsent: func(r *sentRound) { r.env.keepGuidance(r.ctx, r.ref, r.guidance) },
	}
	// recordsNudge keys the nudged event by its turn, so a turn that posted
	// nothing does not hold the next nudge.
	recordsNudge = effectRun{sent: func(r *sentRound) {
		r.env.recordTurnEvent(r.ctx, r.ref, dinv.KindNudged, r.id, strings.Join(inputList(r.inputs[nudgedWho]), ", "))
	}}
	markAddressed = effectRun{state: func(r *sentRound, st *round.Store) { r.env.recordAddressed(st, r.ref, r.pr.HeadRefOid) }}
)

// onSent is the turn effects the command line runs, in the order each
// phase runs them. The daemon records the round-sent event of a round asked
// through its action.
var onSent = []onSentEffect{
	{name: builtin.EffectGuidanceHanded, on: map[route]effectRun{routeSession: keepsGuidance, routeAsked: keepsGuidance}},
	{name: builtin.EffectPRRoundSent, on: map[route]effectRun{routeSession: {sent: func(r *sentRound) {
		r.env.recordEvent(r.ctx, r.ref, dinv.KindRoundSent, withInstructions(handInSession, r.id))
	}}}},
	{name: builtin.EffectPRNudged, on: map[route]effectRun{routeSession: recordsNudge, routeAsked: recordsNudge, routeLaunch: recordsNudge}},
	{name: builtin.EffectPRReviewed, on: map[route]effectRun{routeLaunch: {
		sent: func(r *sentRound) {
			if r.submitted {
				r.fingerprint = r.env.reviewFingerprint(r.ctx, r.cfg, r.ref)
			}
		},
		state:    func(r *sentRound, st *round.Store) { st.SetMark(r.ref.String(), round.Reviewed, r.fingerprint) },
		writesIf: func(r *sentRound) bool { return r.fingerprint != "" },
	}}},
	{name: builtin.EffectPRAddressed, on: map[route]effectRun{routeSession: markAddressed, routeAsked: markAddressed, routeLaunch: markAddressed}},
}

type phase func(c effectRun, r *sentRound)

func guidePhase(c effectRun, r *sentRound) { runCell(c.guide, r) }

func sentPhase(c effectRun, r *sentRound) { runCell(c.sent, r) }

func unsentPhase(c effectRun, r *sentRound) { runCell(c.unsent, r) }

func statePhase(st *round.Store) phase {
	return func(c effectRun, r *sentRound) {
		if c.writes(r) {
			c.state(r, st)
		}
	}
}

func (c effectRun) writes(r *sentRound) bool {
	return c.state != nil && (c.writesIf == nil || c.writesIf(r))
}

func runCell(f func(r *sentRound), r *sentRound) {
	if f != nil {
		f(r)
	}
}

// runPhase runs p of the turn effects d names, on route rt.
func runPhase(rt route, d dintent.Def, r *sentRound, p phase) {
	for _, e := range onSent {
		if slices.Contains(d.Effects, e.name) {
			p(e.on[rt], r)
		}
	}
}

// writesRounds is whether the state phase of d's effects on rt writes the
// round state for r.
func writesRounds(rt route, d dintent.Def, r *sentRound) bool {
	for _, e := range onSent {
		if slices.Contains(d.Effects, e.name) && e.on[rt].writes(r) {
			return true
		}
	}
	return false
}

// roundRun is a round of an intent about to go out in the workspace its
// manifest names; r is the request with its pull request read.
type roundRun struct {
	def dintent.Def
	cfg config.Config
	r   turn.Request
	pr  *github.PullRequest
	// resumeOf is the turn a resume continues, 0 for a new round.
	resumeOf int64
	// st is the round state of a round asked for by hand; nil on a resume.
	st *round.Store
	// rememberHome is the agent home to keep for the pull request's next
	// rounds, "" for none.
	rememberHome string
}

// workspaceRun sends the round in the workspace its builtin names and says
// whether the agent was seen taking it.
type workspaceRun func(e *Env, ctx context.Context, run roundRun) (turnID string, submitted bool, err error)

// workspaceRuns are the workspace builtins the command line implements; a
// given workspace is the one a launch names, and only the probe launch does.
func workspaceRuns() map[string]workspaceRun {
	return map[string]workspaceRun{
		workspaceReview:                 (*Env).runInReview,
		workspaceAuthor:                 (*Env).runInAuthor,
		builtin.WorkspaceLinkedElseMain: (*Env).runInPRElseMain,
	}
}

// policyRound is the policy of a round whose manifest is not at hand: the
// sweep's.
const policyRound = builtin.PolicyRound

// policies are the agent policies a manifest's policy names, for a session
// of intent; none may run or read the managers' control surface.
var policies = map[string]func(e *Env, intent dintent.Name, control turns.Control) turns.Policy{
	policyRound: func(e *Env, _ dintent.Name, control turns.Control) turns.Policy {
		return roundPolicy(e.loopCommand(), prIntents(e.loadedIntents().Registry)).Denied(control)
	},
	builtin.PolicyProbe: func(e *Env, intent dintent.Name, control turns.Control) turns.Policy {
		return probePolicy(e.loopCommand(), []string{string(intent)}).Denied(control)
	},
}

// cliBuiltins are the builtin names the command line implements: the
// workspaces and befores a manifest names, the turn effects, and what a
// role binds.
func cliBuiltins() map[string][]string {
	return map[string][]string{
		string(builtin.SlotWorkspace): append(slices.Sorted(maps.Keys(workspaceRuns())), "given"),
		string(builtin.SlotBefore):    {beforeTakeOver},
		"effects":                     onSentNames(),
		"policy":                      slices.Sorted(maps.Keys(policies)),
		"context":                     {contextPR, builtin.ContextSynthetic},
		"launch":                      {"", builtin.LaunchProbe},
		"reconcile":                   {"", turns.ReconcileMyReview},
	}
}

func onSentNames() []string {
	names := make([]string, len(onSent))
	for i, e := range onSent {
		names[i] = e.name
	}
	return names
}

// options are the session options the round's manifest asks for.
func (run roundRun) options(opts roundOptions) roundOptions {
	return intentOptions(run.def, opts)
}

// intentOptions are the session options d's manifest asks for.
func intentOptions(d dintent.Def, opts roundOptions) roundOptions {
	opts.title, opts.newSession = d.Session.Title, d.Session.Strategy == sessionNew
	opts.policy, opts.intent = d.Policy, d.Name
	return opts
}

// sessionNew is the session strategy that never reuses the live session.
const sessionNew = "new"

// sessionTime is the time in the title of the sessions the loop starts.
const sessionTime = "02/01 15:04"

// sessionTitle is the title of a session the loop starts for a turn of intent:
// its manifest's session title and the time.
func (e *Env) sessionTitle(intent turn.Intent) string {
	d, _ := e.loadedIntents().Registry.Get(dintent.Name(intent))
	return d.Session.Title + " " + e.Now().Format(sessionTime)
}

// beforeSet is whether a set input of the request runs the before builtin.
func beforeSet(d dintent.Def, inputs map[string]any, before string) bool {
	for _, in := range d.Inputs {
		if in.Before == before && inputSet(inputs[in.Name]) {
			return true
		}
	}
	return false
}

func inputSet(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case []string:
		return len(v) > 0
	case []any:
		return len(v) > 0
	}
	return true
}
