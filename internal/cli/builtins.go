package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/wloophq/wloop/internal/domain/pr"

	src "github.com/wloophq/wloop/pkg/plugin/source"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/plugins/bundled"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/transport/mcp"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
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

// pluginEffects are the turn effects of d the plugin of u applies.
func pluginEffects(d dintent.Def, u unit.Unit) []string {
	var names []string
	for _, name := range d.Effects {
		hosts := slices.ContainsFunc(hostEffects, func(h hostEffect) bool { return h.name == name })
		if fx, ok := builtin.EffectOf(name); ok && !hosts && fx.Plugin && fx.Provider == u.Provider && fx.Applies&builtin.AppliesTurn != 0 {
			names = append(names, name)
		}
	}
	return names
}

// checkEffects stops, before a turn of d on u goes out, one whose plugin
// effects no running plugin applies: src.ErrUnavailable, since its plugin
// may run again.
func (e *Env) checkEffects(d dintent.Def, u unit.Unit) error {
	names := pluginEffects(d, u)
	if len(names) == 0 {
		return nil
	}
	if _, ok := e.turnEffects(u.Provider); !ok {
		return fmt.Errorf("%s: no running plugin applies %s; the turn was not sent: %w", u.Label(), strings.Join(names, ", "), src.ErrUnavailable)
	}
	return nil
}

// applyPluginEffects applies, through ApplyEffect, the turn effects of d the
// plugin of u applies, to the turn sent describes. The turn is out
// whatever they answer: an effect that fails, or that its plugin does not
// apply, is a health problem the daemon shows until the next turn of the
// unit applies it.
func (e *Env) applyPluginEffects(ctx context.Context, d dintent.Def, u unit.Unit, sent src.EffectRequest, turnInputs map[string]any) {
	names := pluginEffects(d, u)
	if len(names) == 0 {
		return
	}
	a, ok := e.turnEffects(u.Provider)
	if !ok {
		for _, name := range names {
			e.effectProblems.set(u, name, fmt.Sprintf("%s: no running plugin applied %s to the turn that went out", u.Label(), name))
		}
		return
	}
	var inputs json.RawMessage
	if len(turnInputs) > 0 {
		var err error
		if inputs, err = json.Marshal(turnInputs); err != nil {
			fmt.Fprintf(e.Err, "warning: %s: the turn's inputs: %v\n", u.Label(), err)
		}
	}
	for _, name := range names {
		req := sent
		req.Effect, req.Unit, req.Inputs = name, psource.Wire(u), inputs
		err := a.ApplyEffect(context.WithoutCancel(ctx), req)
		switch {
		case errors.Is(err, src.ErrUnsupported):
			e.effectProblems.set(u, name, fmt.Sprintf("%s: the plugin applies no %s, which the turn that went out names: %v", u.Label(), name, err))
		case err != nil:
			e.effectProblems.set(u, name, fmt.Sprintf("%s: applying %s to the turn that went out: %v", u.Label(), name, err))
			fmt.Fprintf(e.Err, "warning: %s: applying %s: %v\n", u.Label(), name, err)
		default:
			e.effectProblems.set(u, name, "")
		}
	}
}

// effectProblems are the turn effects that went unapplied, by unit and
// effect, which the daemon's health lists.
type effectProblems struct {
	mu sync.Mutex
	m  map[string]string
}

func (p *effectProblems) set(u unit.Unit, effect, text string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	key := u.String() + " " + effect
	if text == "" {
		delete(p.m, key)
		return
	}
	if p.m == nil {
		p.m = map[string]string{}
	}
	p.m[key] = text
}

// list is every problem, sorted.
func (p *effectProblems) list() []string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Sorted(maps.Values(p.m))
}

// roundRun is a round of an intent about to go out in the workspace its
// manifest names; r is the request with its pull request read.
type roundRun struct {
	def dintent.Def
	cfg config.Config
	r   turn.Request
	pr  *pr.PullRequest
	// turn is the turn the round records: a new one its start admitted, or
	// a resume.
	turn turns.Turn
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
	policyRound: func(e *Env, intent dintent.Name, control turns.Control) turns.Policy {
		p := roundPolicy(e.loopCommand(), prIntents(e.loadedIntents().Registry))
		p.DraftTools = draftTools(e.loadedSources())
		return e.withoutSourceWrites(intent, p).Denied(control)
	},
	builtin.PolicyProbe: func(e *Env, intent dintent.Name, control turns.Control) turns.Policy {
		return e.withoutSourceWrites(intent, probePolicy(e.loopCommand(), []string{string(intent)})).Denied(control)
	},
}

// withoutSourceWrites is p denying, when intent's manifest sets source_writes = false, every command a loaded
// plugin's manifest declares as a write to its source.
func (e *Env) withoutSourceWrites(intent dintent.Name, p turns.Policy) turns.Policy {
	d, ok := e.loadedIntents().Registry.Get(intent)
	if !ok {
		return p
	}
	return p.WithoutSourceWrites(d.Session, writeCommands(e.loadedSources()))
}

// writeCommands are the command prefixes srcs declare as writes to their sources, as words.
func writeCommands(srcs []*manifest.Source) [][]string {
	var out [][]string
	for _, s := range srcs {
		for _, c := range s.Session.WriteCommands {
			if words := strings.Fields(c); len(words) > 0 && !slices.ContainsFunc(out, func(o []string) bool { return slices.Equal(o, words) }) {
				out = append(out, words)
			}
		}
	}
	return out
}

// draftTools are the tools a session may call without a prompt because they only propose or read drafts the
// user approves: those srcs mark so, and the loop's own draft tools, as an agent names them.
func draftTools(srcs []*manifest.Source) []string {
	var out []string
	for _, s := range srcs {
		for _, t := range s.Tools {
			if t.Proposes {
				out = append(out, brand.ToolPrefix()+t.Name)
			}
		}
	}
	return append(out, mcp.DraftTools()...)
}

// atOnceTools are the tools srcs mark posts = "now", which post as the user at once, as an agent names them.
func atOnceTools(srcs []*manifest.Source) []string {
	var out []string
	for _, s := range srcs {
		for _, t := range s.Tools {
			if t.Posts == "now" {
				out = append(out, brand.ToolPrefix()+t.Name)
			}
		}
	}
	return out
}

// loadedSources are the [source] tables of every plugin: the discovered plugin's over the bundled one.
func (e *Env) loadedSources() []*manifest.Source {
	srcs, _ := bundled.Sources()
	for _, p := range e.plugins {
		s, err := p.Manifest.Source()
		if err != nil {
			continue
		}
		srcs = slices.DeleteFunc(srcs, func(b *manifest.Source) bool { return b.Provider == s.Provider })
		srcs = append(srcs, s)
	}
	return srcs
}

// cliBuiltins are the builtin names the command line implements: the
// workspaces and befores a manifest names, the turn effects, and what a
// role binds.
func cliBuiltins() map[string][]string {
	return map[string][]string{
		string(builtin.SlotWorkspace): append(slices.Sorted(maps.Keys(workspaceRuns())), "given"),
		string(builtin.SlotBefore):    {beforeTakeOver},
		"effects":                     hostEffectNames(),
		"policy":                      slices.Sorted(maps.Keys(policies)),
		"context":                     {contextPR, builtin.ContextSynthetic},
		"launch":                      {"", builtin.LaunchProbe},
		"reconcile":                   {"", turns.ReconcileMyReview},
	}
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
