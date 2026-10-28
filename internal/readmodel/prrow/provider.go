package prrow

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/intake"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
	"github.com/wloophq/wloop/internal/situation"
)

// Store is what the rows read of the inventory.
type Store interface {
	PRsWithOverrides(ctx context.Context) ([]dinv.PR, map[github.Ref]dinv.Override, error)
	EventsByPR(ctx context.Context, refs []github.Ref) (map[github.Ref][]dinv.Event, error)
	BriefRecords(ctx context.Context, refs ...github.Ref) (map[github.Ref]dinv.BriefRecord, error)
}

// Briefs reads the brief records with their summaries.
type Briefs interface {
	Briefed(ctx context.Context) (map[github.Ref]brief.Summary, map[github.Ref]dinv.BriefRecord, error)
	// AttemptOf is the brief attempt a first turn of pr waits on.
	AttemptOf(pr dinv.PR, rec dinv.BriefRecord) int
}

// Source is where the provider reads rows from: the daemon's RowDeps, the
// inventory and the brief queue. Briefs may be nil: the brief off.
type Source struct {
	readmodel.RowDeps
	Store  Store
	Briefs Briefs
}

// Provider is the github provider of the read model: one row per pull
// request of the inventory, but lapsed ones nobody adopted.
type Provider struct {
	src Source

	mu     sync.Mutex
	listed *listing // what Listed read, for the Load that follows
}

type listing struct {
	prs       map[unit.Unit]dinv.PR
	overrides map[github.Ref]dinv.Override
}

// New is the provider over src.
func New(src Source) *Provider {
	if src.Logf == nil {
		src.Logf = func(string, ...any) {}
	}
	return &Provider{src: src}
}

var _ readmodel.Provider = (*Provider)(nil)

func (p *Provider) Provider() unit.ProviderID { return unit.GitHub }

// Listed lists every pull request of the inventory but the lapsed ones
// nobody adopted, the most recently updated first.
func (p *Provider) Listed(ctx context.Context) ([]unit.Unit, error) {
	prs, overrides, err := p.src.Store.PRsWithOverrides(ctx)
	if err != nil {
		return nil, err
	}
	l := &listing{prs: make(map[unit.Unit]dinv.PR, len(prs)), overrides: overrides}
	units := make([]unit.Unit, 0, len(prs))
	for _, pr := range prs {
		if pr.Lapsed() && !overrides[pr.Ref].Adopted {
			continue
		}
		u := pr.Ref.Unit()
		l.prs[u] = pr
		units = append(units, u)
	}
	p.mu.Lock()
	p.listed = l
	p.mu.Unlock()
	return units, nil
}

// input is what one pull request's row derives from.
type input struct {
	pr       dinv.PR
	events   []dinv.Event
	override dinv.Override
	turn     *TurnFact
	// session is the started turn's session; sessionErr says it could
	// not be read.
	session    *turn.Session
	sessionErr bool
	intakes    []intake.Intake
	intake     *api.Intake
	brief      *api.BriefSummary
	activity   *turn.Activity
	book       *turn.Book
	set        *shared
}

type shared struct {
	readmodel.RowSettings
	briefOn bool
}

// Load reads what the rows of units derive from, in one query per kind of
// input.
func (p *Provider) Load(ctx context.Context, units []unit.Unit) (map[unit.Unit]any, error) {
	p.mu.Lock()
	l := p.listed
	p.listed = nil
	p.mu.Unlock()
	if l == nil {
		if _, err := p.Listed(ctx); err != nil {
			return nil, err
		}
		p.mu.Lock()
		l, p.listed = p.listed, nil
		p.mu.Unlock()
	}
	set := &shared{RowSettings: p.src.Settings(), briefOn: p.src.Briefs != nil}

	ins := make(map[unit.Unit]*input, len(units))
	refs := make([]github.Ref, 0, len(units))
	for _, u := range units {
		pr, ok := l.prs[u]
		if !ok {
			continue
		}
		o, ok := l.overrides[pr.Ref]
		if !ok {
			o.PR = pr.Ref
		}
		ins[u] = &input{pr: pr, override: o, set: set}
		refs = append(refs, pr.Ref)
	}
	events, err := p.src.Store.EventsByPR(ctx, refs)
	if err != nil {
		return nil, err
	}
	for _, in := range ins {
		in.events = events[in.pr.Ref]
	}
	if err := p.loadTurns(ctx, ins, set); err != nil {
		return nil, err
	}
	var recs map[github.Ref]dinv.BriefRecord
	if p.src.Briefs != nil {
		var sums map[github.Ref]brief.Summary
		if sums, recs, err = p.src.Briefs.Briefed(ctx); err != nil {
			return nil, err
		}
		for _, in := range ins {
			if b, ok := sums[in.pr.Ref]; ok {
				in.brief = BriefSummary(b)
			}
		}
	}
	if err := p.loadIntakes(ctx, ins, recs); err != nil {
		return nil, err
	}
	acts, err := p.activities(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]any, len(ins))
	for u, in := range ins {
		if a, ok := acts[u]; ok {
			in.activity = &a
		}
		out[u] = in
	}
	return out, nil
}

// loadTurns reads every book once and, when a turn is started, the open
// sessions; a started turn's session the list lacks is read alone.
func (p *Provider) loadTurns(ctx context.Context, ins map[unit.Unit]*input, set *shared) error {
	if p.src.Books == nil {
		return nil
	}
	books, err := p.src.Books.Books(ctx)
	if err != nil {
		return err
	}
	facts := TurnFacts(books, set.Loaded.Registry)
	var sessions map[string]turn.Session
	if Started(facts) && p.src.Sessions != nil {
		open, err := p.src.Sessions.Sessions(ctx)
		if err != nil {
			return err
		}
		sessions = make(map[string]turn.Session, len(open))
		for _, s := range open {
			sessions[s.ID] = s
		}
	}
	for _, in := range ins {
		f, ok := facts[in.pr.Ref]
		if !ok {
			continue
		}
		in.turn = &f
		b := books[in.pr.Ref.Unit()]
		in.book = &b
		if f.Session == "" || sessions == nil {
			continue
		}
		s, ok := sessions[f.Session]
		if !ok {
			var err error
			if s, ok, err = p.src.Sessions.Session(ctx, f.Session); err != nil {
				p.src.Logf("rows: reading session %s: %v", f.Session, err)
				in.sessionErr = true
				continue
			}
		}
		if ok {
			in.session = &s
		}
	}
	return nil
}

// loadIntakes reads the open intakes and, when recs is nil and any is
// open, their pull requests' brief records in one query.
func (p *Provider) loadIntakes(ctx context.Context, ins map[unit.Unit]*input, recs map[github.Ref]dinv.BriefRecord) error {
	if p.src.Intakes == nil {
		return nil
	}
	open, err := p.src.Intakes(ctx)
	if err != nil {
		return err
	}
	if len(open) > 0 && recs == nil {
		refs := make([]github.Ref, 0, len(open))
		for _, in := range open {
			if ref, err := pr.ParseKey(in.Unit.Key); err == nil && in.Unit.Provider == unit.GitHub {
				refs = append(refs, ref)
			}
		}
		if recs, err = p.src.Store.BriefRecords(ctx, refs...); err != nil {
			return err
		}
	}
	byUnit := map[unit.Unit][]intake.Intake{}
	for _, i := range open {
		byUnit[i.Unit] = append(byUnit[i.Unit], i)
	}
	for _, in := range ins {
		in.intakes = byUnit[in.pr.Ref.Unit()]
		if len(in.intakes) > 0 && p.src.Intake != nil {
			rec, found := recs[in.pr.Ref]
			w := p.src.Intake(in.intakes[0], BriefState(rec, found), p.attemptOf(in.pr, rec))
			in.intake = &w
		}
	}
	return nil
}

// BriefState is what an intake shows of the brief record rec, found false
// for none.
func BriefState(rec dinv.BriefRecord, found bool) intake.BriefState {
	if !found {
		return intake.BriefState{}
	}
	b := intake.BriefState{Found: true, Failures: rec.Failures, GaveUp: !rec.GaveUpAt.IsZero(), RetryAt: rec.RetryAt}
	if rec.LastRun != 0 && rec.LastRun != rec.Run {
		b.LastError = rec.Error
	}
	return b
}

// attemptOf is the brief attempt an intake of pr waits on.
func (p *Provider) attemptOf(pr dinv.PR, rec dinv.BriefRecord) func() (int, error) {
	return func() (int, error) {
		if p.src.Briefs == nil {
			return 0, errNoBriefs
		}
		return p.src.Briefs.AttemptOf(pr, rec), nil
	}
}

var errNoBriefs = errors.New("prrow: briefs are off")

// Derive is the row of one pull request at the minute at.
func (p *Provider) Derive(base readmodel.Base, in any, at time.Time) (api.Row, error) {
	x, ok := in.(*input)
	if !ok {
		return api.Row{}, fmt.Errorf("prrow: input %T", in)
	}
	set := x.set
	d := DeriverAt(base, set.RowSettings, set.briefOn, at)
	ref := x.pr.Ref
	if f, ok := x.turnFact(base); ok {
		d.Turns = map[github.Ref]TurnFact{ref: f}
	}
	if len(x.intakes) > 0 {
		d.Intakes = map[github.Ref][]intake.Intake{ref: x.intakes}
	}
	r, pl := d.Parts(x.pr, x.events, x.override)
	pl.Brief = x.brief
	pl.Intake = x.intake
	if x.activity != nil {
		d.AddActivity(&pl, *x.activity)
	}
	return d.Seal(r, pl), nil
}

// DeriverAt is the deriver of a request: base and the settings, at the
// minute at, with no turn and no intake of any pull request.
func DeriverAt(base readmodel.Base, set readmodel.RowSettings, briefOn bool, at time.Time) Deriver {
	return Deriver{Viewer: base.Viewers[unit.GitHub], Teams: base.Teams[unit.GitHub], Ignore: IgnoreOf(set.Cfg), Now: at, Stalled: set.Cfg.GitHub.Stalled,
		Pending: base.Pending, Loaded: set.Loaded, Specs: base.Specs, Cfg: set.Cfg, GateOn: set.GateOn, BriefOn: briefOn,
		Forceable: set.Forceable, SpecsVersion: cmp.Or(base.SpecsVersion, catalog.VersionOf(set.Loaded)), Asked: base.Asked}
}

// PROf is the pull request a row's input derives from.
func PROf(in any) (dinv.PR, bool) {
	x, ok := in.(*input)
	if !ok {
		return dinv.PR{}, false
	}
	return x.pr, true
}

// EventsOf are the events a row's input holds, oldest first; the caller
// must not change them.
func EventsOf(in any) []dinv.Event {
	if x, ok := in.(*input); ok {
		return x.events
	}
	return nil
}

// BookOf is the turn book of the input's pull request, when it has one.
func BookOf(in any) (turn.Book, bool) {
	if x, ok := in.(*input); ok && x.book != nil {
		return *x.book, true
	}
	return turn.Book{}, false
}

// Unmuted is the input as it stands with no override at all.
func Unmuted(in any) any {
	x, ok := in.(*input)
	if !ok {
		return in
	}
	c := *x
	c.override = dinv.Override{PR: x.pr.Ref}
	return &c
}

// turnFact is the row's turn fact with what the agents say of its session;
// ok is false when the pull request has no turn.
func (x *input) turnFact(base readmodel.Base) (f TurnFact, ok bool) {
	if x.turn == nil {
		return TurnFact{}, false
	}
	f = *x.turn
	switch {
	case f.Session == "":
	case !base.AgentsOK || x.sessionErr:
		f.Unread = true
	case x.session != nil:
		f.Waiting = overlay.SessionWaiting(*x.session, base.Agents)
	}
	return f, true
}

// Situations are the followed open pull requests whose row's situation,
// "<kind>:<waiting-on>", differs from their newest situation event, seen at
// at. A pull request whose started turn's session could not be read is
// left for a later snapshot, so a failed read never records it as working.
func Situations(k readmodel.Kept, at time.Time) []dinv.Situation {
	var out []dinv.Situation
	for _, b := range k.Rows {
		x, ok := b.In.(*input)
		if !ok || !x.pr.Followed() || x.pr.State != dgh.StateOpen {
			continue
		}
		if f, ok := x.turnFact(k.Base); ok && f.Unread {
			continue
		}
		data := string(b.Row.Kind) + ":" + b.Row.Summary.WaitsOn
		if data != lastSituation(x.events) {
			out = append(out, dinv.Situation{PR: x.pr.Ref, Data: data, At: at})
		}
	}
	return out
}

// lastSituation is the data of the situation event with the highest key,
// "" for none.
func lastSituation(evs []dinv.Event) string {
	var last string
	top := int64(-1)
	for _, e := range evs {
		if e.Kind != dinv.KindSituation {
			continue
		}
		if n, err := strconv.ParseInt(e.Key, 10, 64); err == nil && n > top {
			top, last = n, e.Data
		}
	}
	return last
}

// LapsedMutes are the stored overrides whose "mute until state change"
// lapsed: the rows of k show them unmuted.
func LapsedMutes(k readmodel.Kept) []dinv.Override {
	var out []dinv.Override
	for _, b := range k.Rows {
		x, ok := b.In.(*input)
		if ok && MuteLapsed(x.pr, x.override, situation.Kind(b.Row.Kind)) {
			out = append(out, x.override)
		}
	}
	return out
}

// activities are the pull requests' activity; none without an activity
// reader.
func (p *Provider) activities(ctx context.Context) (map[unit.Unit]turn.Activity, error) {
	if p.src.Activity == nil {
		return nil, nil
	}
	return p.src.Activity.Activities(ctx, unit.GitHub)
}
