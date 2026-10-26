package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

// turnPR is a pull request as a turn reads it.
func turnPR(pr *github.PullRequest) *turn.PR {
	return &turn.PR{Title: pr.Title, Author: pr.Author, State: pr.State, Association: pr.AuthorAssociation, Head: pr.HeadRefOid, Base: pr.BaseRefName, Labels: pr.Labels,
		CrossRepository: pr.IsCrossRepository, Visibility: pr.Visibility, Prior: priorReview(pr.Reviews), ReviewRequested: pr.ViewerRequested, LastReviewVerdict: lastReviewVerdict(pr.Reviews)}
}

// prFacts resolves pr's facts for a predicate over the pull request; nil
// without one.
func prFacts(pr *github.PullRequest) expr.Facts {
	if pr == nil {
		return nil
	}
	p := turnPR(pr)
	p.Owner, p.Repo, p.Number = pr.Owner, pr.Repo, pr.Number
	return turn.PayloadFacts(p)
}

// lastReviewVerdict is the state of your newest submitted review, comments included.
func lastReviewVerdict(reviews []pr.Review) string {
	var state string
	var at time.Time
	for _, r := range reviews {
		if r.ByViewer && !r.SubmittedAt.IsZero() && r.SubmittedAt.After(at) {
			state, at = r.State, r.SubmittedAt
		}
	}
	return state
}

// priorReview is your latest submitted review: the head it covered, and the
// latest verdict that was not a comment. GitHub can return a review without
// its commit; such a review gives no head.
func priorReview(reviews []pr.Review) *turn.PriorReview {
	var p turn.PriorReview
	reviewed := false
	var headAt, verdictAt time.Time
	for _, r := range reviews {
		if !r.ByViewer || r.SubmittedAt.IsZero() {
			continue
		}
		switch r.State {
		case pr.ReviewApproved, pr.ReviewChangesRequested, pr.ReviewCommented, pr.ReviewDismissed:
		default:
			continue
		}
		reviewed = true
		if r.CommitOid != "" && r.SubmittedAt.After(headAt) {
			p.Head, headAt = r.CommitOid, r.SubmittedAt
		}
		if r.SubmittedAt.After(p.At) {
			p.At = r.SubmittedAt
		}
		if r.State != pr.ReviewCommented && r.SubmittedAt.After(verdictAt) {
			p.Verdict, verdictAt = r.State, r.SubmittedAt
		}
	}
	if !reviewed {
		return nil
	}
	return &p
}

// basicsReader reads a turn's pull request with the read rounds already make.
type basicsReader struct{ e *Env }

func (b basicsReader) PR(ctx context.Context, ref github.Ref) (turn.PR, error) {
	pr, err := b.e.basics(ctx, ref)
	if err != nil {
		return turn.PR{}, err
	}
	return *turnPR(pr), nil
}

type sentTurn struct {
	ID, Prompt, Text string
	// Seq is the turn deliver stored; 0 when none is.
	Seq int64
	// send is the turn deliver stores before typing the prompt; nil when the
	// text asks for no record or the process keeps no turns.
	send *turns.SendRequest
}

func (s *sentTurn) resume(seq int64) {
	if s.send != nil {
		s.send.ResumeOf = seq
	}
}

func turnTrigger(t turn.Trigger) dturn.Trigger {
	if t == turn.Automated {
		return dturn.Automated
	}
	return dturn.ByHand
}

// intentRecheck is the production IntentRecheck.
const intentRecheck = 2 * time.Second

type envIntents struct {
	mu     sync.Mutex
	cache  *intentload.Cache
	viewer string
	// layers are the last profile layers read from a config that loaded;
	// configErr is the last config failure reported.
	layers    []profile.Layer
	configErr string
}

// newEnvIntents is the registry cache of e and of every copy of it, over
// the bundled intents and the configured profile's.
func newEnvIntents(e *Env) *envIntents {
	st := &envIntents{}
	c, err := turn.NewCache(func() []profile.Layer { return st.profileLayers(e) }, intentload.CacheOptions{Recheck: e.IntentRecheck, OnReload: func() { e.noteConfig() }})
	if err != nil {
		panic(err)
	}
	st.cache = c
	return st
}

// profileLayers are the configured profile's layers. A config that fails to
// load keeps the last layers that loaded, and the failure is reported once.
func (st *envIntents) profileLayers(e *Env) []profile.Layer {
	if e.cfg != nil {
		return profile.Chain(e.cfg.Config())
	}
	if e.LoadConfig == nil {
		return profile.Chain(config.Config{})
	}
	cfg, err := e.loadConfig()
	st.mu.Lock()
	defer st.mu.Unlock()
	if err == nil {
		st.layers, st.configErr = profile.Chain(cfg), ""
		return st.layers
	}
	if err.Error() != st.configErr {
		st.configErr = err.Error()
		if e.Err != nil {
			fmt.Fprintf(e.Err, "warning: intents: the config does not load, so the profile's intents are the last ones read: %v\n", err)
		}
	}
	if st.layers == nil {
		return profile.Chain(config.Config{})
	}
	return st.layers
}

// intentState is the Env's registry state; an Env built outside newEnv gets its own.
func (e *Env) intentState() *envIntents {
	if e.intents == nil {
		e.intents = newEnvIntents(e)
	}
	return e.intents
}

// loadedIntents is the registry as the profile defines it now.
func (e *Env) loadedIntents() *intentload.Loaded { return e.intentState().cache.Get() }

// intentDef is the registry's definition of name, or the error that says
// why there is none.
func (e *Env) intentDef(name string) (dintent.Def, error) {
	l := e.loadedIntents()
	if d, ok := l.Registry.Get(dintent.Name(name)); ok {
		return d, nil
	}
	if p, ok := l.Broken[dintent.Name(name)]; ok {
		return dintent.Def{}, fmt.Errorf("the intent %s has problems:\n%s", name, p.Error())
	}
	return dintent.Def{}, fmt.Errorf("unknown intent %q; use %s", name, l.Registry.Names())
}

// historyArtifact is the artifact the history of a turn of intent quotes.
func (e *Env) historyArtifact(intent string) string {
	rules, _ := e.loadedIntents().Registry.Rules(intent)
	return rules.HistoryArtifact
}

// registryPort gives the turn service the registry as the profile defines it at each call.
type registryPort struct{ e *Env }

var _ turns.Intents = registryPort{}

func (p registryPort) reg() *dintent.Registry { return p.e.loadedIntents().Registry }

func (p registryPort) Rules(intent string) (dintent.RecordRules, error) {
	return p.e.loadedIntents().Rules(intent)
}
func (p registryPort) Answerable() []string              { return p.reg().Answerable() }
func (p registryPort) ClosesWhenDone(intent string) bool { return p.reg().ClosesWhenDone(intent) }
func (p registryPort) Reconcile(intent string) string    { return p.reg().Reconcile(intent) }
func (p registryPort) SessionTitles() []string           { return p.reg().SessionTitles() }
func (p registryPort) All() []dintent.Def                { return p.reg().All() }

// viewerLogin is the user's GitHub login, read once.
func (e *Env) viewerLogin(ctx context.Context) (string, error) {
	st := e.intentState()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.viewer != "" {
		return st.viewer, nil
	}
	login := aheadViewer(ctx)
	var err error
	if login == "" {
		login, err = e.storedViewer(ctx)
	}
	if err != nil || login == "" {
		if login, err = e.GitHub.Viewer(ctx); err != nil {
			return "", err
		}
	}
	st.viewer = login
	return login, nil
}

// storedViewer is the login the daemon's collector stored; "" without a store
// or before the first slow sweep.
func (e *Env) storedViewer(ctx context.Context) (string, error) {
	if e.Store == nil {
		return "", nil
	}
	return e.Store.Viewer(ctx, unit.GitHub)
}

func (e *Env) turnBuilder(cfg config.Config) turn.Builder {
	l := e.loadedIntents()
	b := turn.Builder{Payloads: e.payloadReaders(), Guides: profile.Guides{Tree: l.Tree}, Intents: func() *intentload.Loaded { return l },
		Viewer: e.viewerLogin, Config: cfg, Command: e.loopCommand(), ManagerBinary: e.controlBinary(cfg), Items: e.itemsReader(), Dossiers: e.dossierReader()}
	switch {
	case e.Store != nil:
		b.Facts = storedFacts{store: e.Store, briefs: cfg.Brief.Enabled}
	case e.facts != nil:
		b.Facts = e.facts
	}
	return b
}

// renderTurn builds and renders a turn without keeping it.
func (e *Env) renderTurn(ctx context.Context, cfg config.Config, r turn.Request) (turn.Context, turn.Rendered, error) {
	return e.renderTurnBy(ctx, cfg, r, e.turnBuilder)
}

// renderTurnBy is renderTurn with the builder build makes for the turn's configuration.
func (e *Env) renderTurnBy(ctx context.Context, cfg config.Config, r turn.Request, build func(config.Config) turn.Builder) (turn.Context, turn.Rendered, error) {
	cfg = cfg.For(prOf(r.Unit).Owner, prOf(r.Unit).Repo)
	if r.Agent == "" {
		r.Agent = cfg.Agent
	}
	if r.BriefMissing == "" {
		r.BriefMissing = daemon.BriefMissing(ctx)
	}
	if !cfg.Brief.Enabled && r.BriefMissing == "" {
		r.BriefMissing = "briefs are off (brief.enabled in the config)"
	}
	c, err := build(cfg).Build(ctx, r)
	if err != nil {
		return c, turn.Rendered{}, err
	}
	out, err := turn.Render(c, e.loadedIntents(), os.Getenv("LOOP_INSTRUCTIONS_DIR"))
	return c, out, err
}

// storeTurn renders a turn and keeps it under its id, so the agent reads by
// id whatever changes afterwards.
func (e *Env) storeTurn(ctx context.Context, cfg config.Config, r turn.Request) (sentTurn, error) {
	c, out, err := e.renderTurn(ctx, cfg, r)
	if err != nil {
		return sentTurn{}, err
	}
	ref := prOf(r.Unit)
	repo := ""
	if r.Unit.Provider == unit.GitHub {
		repo = ref.Owner + "/" + ref.Repo
	}
	stored := dinv.StoredTurn{ID: out.ID, Kind: string(r.Intent), Repo: repo,
		Text: out.Text, Sources: out.Sources, BundledSHA: profile.BundledSHA(profile.Kind(c.Def.Profile))}
	if err := e.saveTurnText(ctx, stored); err != nil {
		return sentTurn{}, err
	}
	e.recordTrimmed(ctx, r.Unit, out.ID, turn.Trimmed(c))
	sent := sentTurn{ID: out.ID, Prompt: turn.Prompt(c, out.ID), Text: out.Text}
	if out.Records && e.Turns != nil {
		sent.send = &turns.SendRequest{Unit: r.Unit, Intent: dturn.Intent(r.Intent), Turn: out.ID, Trigger: turnTrigger(r.Trigger),
			Guidance: r.Guidance, Agent: cfg.For(ref.Owner, ref.Repo).Agent, Records: true, Inputs: persisted(c),
			Items: r.Items, Entry: r.Entry, Role: c.Def.Role}
		if r.Why != nil {
			sent.send.Workflow = r.Why.Workflow
		}
		if pr := c.PR(); pr != nil {
			sent.send.Head = pr.Head
		}
		if c.Brief != nil {
			sent.send.BriefRun = c.Brief.Run
		}
	}
	return sent, nil
}

// persisted are the turn's inputs its manifest keeps with the turn; nil when none.
func persisted(c turn.Context) map[string]any {
	var out map[string]any
	for _, in := range c.Def.Inputs {
		if in.Persist {
			if out == nil {
				out = map[string]any{}
			}
			out[in.Name] = c.Inputs[in.Name]
		}
	}
	return out
}

// kindGuideTrimmed records the blocks a turn's guide left out to stay
// within the judgment limit; Key is the turn's id, Data the blocks.
const kindGuideTrimmed = "guide_trimmed"

func (e *Env) recordTrimmed(ctx context.Context, u unit.Unit, id string, dropped []string) {
	if len(dropped) == 0 || e.Rec == nil {
		return
	}
	ev := port.Event{Unit: u, Kind: kindGuideTrimmed, Key: id, At: e.Now(), Data: strings.Join(dropped, ", ")}
	if err := e.Rec.RecordUnit(context.WithoutCancel(ctx), ev); err != nil {
		fmt.Fprintf(e.Err, "warning: %s: recording %s: %v\n", u.Label(), kindGuideTrimmed, err)
	}
}

func (e *Env) saveTurnText(ctx context.Context, t dinv.StoredTurn) error {
	store, err := e.store()
	if err != nil {
		return err
	}
	return store.SaveTurn(ctx, t, e.Now())
}

// sendTurn is the turn of a round about to go out: it carries the history
// of its turns and the verdict they last published.
func (e *Env) sendTurn(ctx context.Context, cfg config.Config, r turn.Request) (sentTurn, error) {
	if e.Turns != nil {
		books, err := e.Turns.Store.Books(ctx, r.Unit)
		if err != nil {
			return sentTurn{}, err
		}
		b := books[r.Unit]
		if r.History == nil {
			if r.History, err = e.turnHistory(ctx, r.Unit, b); err != nil {
				return sentTurn{}, err
			}
		}
		if v, ok := b.LastVerdict(dturn.Intent(r.Intent)); ok {
			r.LastVerdict = &turn.RecordedVerdict{Head: v.Head, Endorsed: v.Endorsed, At: v.At}
		}
	}
	return e.storeTurn(ctx, cfg, r)
}

// openPR reads the pull request a round is about and refuses one that is
// not open.
func (e *Env) openPR(ctx context.Context, ref github.Ref) (*github.PullRequest, error) {
	pr, err := e.basics(ctx, ref)
	if err != nil {
		return nil, err
	}
	if pr.State != dgh.StateOpen {
		return nil, fmt.Errorf("%s is %s", ref.URL(), pr.State)
	}
	return pr, nil
}
