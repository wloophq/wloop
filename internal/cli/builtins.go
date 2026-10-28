package cli

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	src "github.com/wloophq/wloop/pkg/plugin/source"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/intent/builtin"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/turn"
)

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
}

// effectRun is what a host effect does on a route, by phase: guide before
// the round, unsent when it did not go out.
type effectRun struct {
	guide, unsent func(r *sentRound)
}

// onSentEffect is a turn effect the host applies and what it does on each
// route; every other turn effect is its plugin's, applied through
// ApplyEffect once the round went out.
type onSentEffect struct {
	name string
	on   map[route]effectRun
}

var keepsGuidance = effectRun{
	guide:  func(r *sentRound) { r.guidance = r.env.handGuidance(r.ctx, r.ref, r.guidance) },
	unsent: func(r *sentRound) { r.env.keepGuidance(r.ctx, r.ref, r.guidance) },
}

// onSent is the turn effects the command line applies, in the order each
// phase runs them.
var onSent = []onSentEffect{
	{name: builtin.EffectGuidanceHanded, on: map[route]effectRun{routeSession: keepsGuidance, routeAsked: keepsGuidance}},
}

type phase func(c effectRun, r *sentRound)

func guidePhase(c effectRun, r *sentRound) { runCell(c.guide, r) }

func unsentPhase(c effectRun, r *sentRound) { runCell(c.unsent, r) }

func runCell(f func(r *sentRound), r *sentRound) {
	if f != nil {
		f(r)
	}
}

// runPhase runs p of the host's turn effects d names, on route rt.
func runPhase(rt route, d dintent.Def, r *sentRound, p phase) {
	for _, e := range onSent {
		if slices.Contains(d.Effects, e.name) {
			p(e.on[rt], r)
		}
	}
}

// applyPluginEffects applies, through ApplyEffect, the turn effects of d
// that belong to the plugin of r's unit, once r went out on route rt. A
// failure is only logged: the round is out.
func (e *Env) applyPluginEffects(ctx context.Context, rt route, d dintent.Def, r *sentRound) {
	u := r.ref.Unit()
	var names []string
	for _, name := range d.Effects {
		hosts := slices.ContainsFunc(onSent, func(h onSentEffect) bool { return h.name == name })
		if fx, ok := builtin.EffectOf(name); ok && !hosts && fx.Plugin && fx.Provider == u.Provider && fx.Applies&builtin.AppliesTurn != 0 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return
	}
	a, ok := e.turnEffects(u.Provider, rt, r)
	if !ok {
		fmt.Fprintf(e.Err, "warning: %s: no plugin applies %s\n", r.ref, strings.Join(names, ", "))
		return
	}
	head := ""
	if r.pr != nil {
		head = r.pr.HeadRefOid
	}
	for _, name := range names {
		req := src.EffectRequest{Effect: name, Unit: psource.Wire(u), Turn: &src.TurnRef{ID: r.id, Head: head}}
		if err := a.ApplyEffect(context.WithoutCancel(ctx), req); err != nil {
			fmt.Fprintf(e.Err, "warning: %s: applying %s: %v\n", r.ref, name, err)
		}
	}
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

// roundName is the session name of a round's turn on u; the round's session
// title when it has no intent.
func (e *Env) roundName(opts roundOptions, u unit.Unit) (string, error) {
	if opts.intent == "" {
		return opts.title, nil
	}
	return e.sessionName(opts.intent, u, opts.names)
}

// sessionName is what the manager and the agent show a session the loop
// starts for a turn of intent on u as: its manifest's session.name.
func (e *Env) sessionName(intent dintent.Name, u unit.Unit, names map[string]any) (string, error) {
	facts := maps.Clone(names)
	if facts == nil {
		facts = map[string]any{}
	}
	facts["unit.label"], facts["session.started"] = u.Label(), e.Now()
	return e.loadedIntents().SessionName(intent, facts)
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
