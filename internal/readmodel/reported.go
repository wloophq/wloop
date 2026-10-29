package readmodel

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
	"github.com/wloophq/wloop/internal/plugins/source/value"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Reports reads what source plugins last reported of their units.
type Reports interface {
	// Units are p's units with a snapshot.
	Units(ctx context.Context, p unit.ProviderID) ([]unit.Unit, error)
	// Reported are units' last snapshots; a unit with none is absent.
	Reported(ctx context.Context, units []unit.Unit) (map[unit.Unit]src.Snapshot, error)
}

// Mutes reads the user's mutes of units; a unit with none is absent.
type Mutes interface {
	Mutes(ctx context.Context, units []unit.Unit) (map[unit.Unit]overlay.Mute, error)
}

// WakeMutes are Mutes from the wakes of the units' tasks, as wakes reads
// them by unit.
type WakeMutes func(ctx context.Context, units []unit.Unit) (map[unit.Unit]wake.Wake, error)

// Mutes are the mutes the wakes hold; a spent wake mutes nothing.
func (f WakeMutes) Mutes(ctx context.Context, units []unit.Unit) (map[unit.Unit]overlay.Mute, error) {
	got, err := f(ctx, units)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]overlay.Mute, len(got))
	for u, w := range got {
		if m := overlay.MuteOf(w); m.Set() {
			out[u] = m
		}
	}
	return out, nil
}

// ReportedSource is what a source plugin's rows read: its reports and the
// host's own state. Mutes may be nil: no unit is muted.
type ReportedSource struct {
	RowDeps
	ID      unit.ProviderID
	Reports Reports
	Mutes   Mutes
	// Viewer is the user as the plugin's people are keyed, whom a card
	// waiting on the user names; nil names nobody.
	Viewer func(ctx context.Context) items.Person
	// Stale says u's plugin no longer stands behind its last report; nil
	// says none is.
	Stale func(u unit.Unit) bool
	// Catalog is the plugin's views, facets and situations; nil serves none.
	Catalog Presenter
}

// Reported serves a source plugin's units as rows: each unit's last
// snapshot under the host's overlays, with no call to the plugin. The
// overlays are the run that drives the row, the mute and the pending round;
// an open intake shows in the row's line.
type Reported struct{ src ReportedSource }

var (
	_ Provider  = (*Reported)(nil)
	_ Presenter = (*Reported)(nil)
)

// NewReported is the provider of src's units.
func NewReported(src ReportedSource) *Reported {
	if src.Logf == nil {
		src.Logf = func(string, ...any) {}
	}
	return &Reported{src: src}
}

func (p *Reported) Provider() unit.ProviderID { return p.src.ID }

func (p *Reported) Views() []api.ViewSpec {
	if p.src.Catalog == nil {
		return nil
	}
	return p.src.Catalog.Views()
}

func (p *Reported) Facets() []api.FacetSpec {
	if p.src.Catalog == nil {
		return nil
	}
	return p.src.Catalog.Facets()
}

func (p *Reported) Events() []api.EventSpec {
	if ep, ok := p.src.Catalog.(EventPresenter); ok {
		return ep.Events()
	}
	return nil
}

func (p *Reported) Details() []api.DetailSpec {
	if ep, ok := p.src.Catalog.(EventPresenter); ok {
		return ep.Details()
	}
	return nil
}

func (p *Reported) Situations() []api.SituationSpec {
	if p.src.Catalog == nil {
		return nil
	}
	return p.src.Catalog.Situations()
}

// Listed are the units the plugin reported.
func (p *Reported) Listed(ctx context.Context) ([]unit.Unit, error) {
	return p.src.Reports.Units(ctx, p.src.ID)
}

// reported is what one unit's row derives from.
type reported struct {
	unit unit.Unit
	snap src.Snapshot
	run  *overlay.Run
	// session is the started turn's session; sessionErr says it could not
	// be read.
	session    *turn.Session
	sessionErr bool
	mute       *overlay.Mute
	intake     *api.Intake
	// unmuted derives the row as if the user had not muted the unit.
	unmuted bool
	stale   bool
}

// Load reads each unit's last snapshot, its turn book and session, its mute
// and its open intake. A unit with no snapshot has no row.
func (p *Reported) Load(ctx context.Context, units []unit.Unit) (map[unit.Unit]any, error) {
	snaps, err := p.src.Reports.Reported(ctx, units)
	if err != nil {
		return nil, err
	}
	ins := make(map[unit.Unit]*reported, len(snaps))
	listed := slices.SortedFunc(maps.Keys(snaps), api.CompareUnits)
	for _, u := range listed {
		ins[u] = &reported{unit: u, snap: snaps[u], stale: p.src.Stale != nil && p.src.Stale(u)}
	}
	if err := p.loadRuns(ctx, ins, listed); err != nil {
		return nil, err
	}
	if p.src.Mutes != nil {
		mutes, err := p.src.Mutes.Mutes(ctx, listed)
		if err != nil {
			return nil, err
		}
		for u, m := range mutes {
			if in, ok := ins[u]; ok {
				in.mute = &m
			}
		}
	}
	if err := p.loadIntakes(ctx, ins); err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]any, len(ins))
	for u, in := range ins {
		out[u] = in
	}
	return out, nil
}

func (p *Reported) loadRuns(ctx context.Context, ins map[unit.Unit]*reported, units []unit.Unit) error {
	if p.src.Books == nil || len(units) == 0 {
		return nil
	}
	books, err := p.src.Books.Books(ctx, units...)
	if err != nil {
		return err
	}
	reg := p.src.Settings().Loaded.Registry
	for u, b := range books {
		in, ok := ins[u]
		if !ok {
			continue
		}
		run, ok := overlay.RunOf(b, reg)
		if !ok {
			continue
		}
		in.run = &run
		if run.Session == "" || p.src.Sessions == nil {
			continue
		}
		s, found, err := p.src.Sessions.Session(ctx, run.Session)
		if err != nil {
			p.src.Logf("rows: reading session %s: %v", run.Session, err)
			in.sessionErr = true
			continue
		}
		if found {
			in.session = &s
		}
	}
	return nil
}

var errNoBrief = errors.New("readmodel: a reported unit's brief attempt is the plugin's")

func (p *Reported) loadIntakes(ctx context.Context, ins map[unit.Unit]*reported) error {
	if p.src.Intakes == nil || p.src.Intake == nil {
		return nil
	}
	open, err := p.src.Intakes(ctx)
	if err != nil {
		return err
	}
	for _, i := range open {
		in, ok := ins[i.Unit]
		if !ok || in.intake != nil {
			continue
		}
		w := p.src.Intake(i, intake.BriefState{}, func() (int, error) { return 0, errNoBrief })
		in.intake = &w
	}
	return nil
}

// Unmuted is in, a reported unit's row input, derived as if the user had
// not muted it; in as it is for another provider's input.
func Unmuted(in any) any {
	x, ok := in.(*reported)
	if !ok {
		return in
	}
	c := *x
	c.unmuted = true
	return &c
}

// Unmuted is in derived as if the user had not muted its unit.
func (p *Reported) Unmuted(in any) any { return Unmuted(in) }

// Derive is the row of one reported unit at at.
func (p *Reported) Derive(base Base, in any, at time.Time) (api.Row, error) {
	x, ok := in.(*reported)
	if !ok {
		return api.Row{}, fmt.Errorf("reported rows: input %T", in)
	}
	s := x.snap
	sit := x.situation(base, at)
	reportedKind := ""
	if s.Situation != nil {
		reportedKind = s.Situation.Kind
	}
	r := api.Row{Label: x.unit.Label(), Kind: api.Kind(sit.Kind), Views: slices.Clone(s.Views), Facets: map[string][]string{},
		Payload: s.Data}
	if c := s.Card; c != nil {
		r.Link, r.Title = c.Link, c.Title
		if c.Label != "" {
			r.Label = c.Label
		}
	}
	if r.Views == nil {
		r.Views = []string{}
	}
	for id, v := range s.Facets {
		r.Facets[id] = []string{v}
	}
	for id, vs := range s.FacetValues {
		r.Facets[id] = slices.Clone(vs)
	}
	for _, it := range s.Items {
		r.Items = append(r.Items, api.RowItem{ID: it.ID, Kind: it.Kind, Summary: it.Summary, State: it.State,
			WaitingOn: personKeys(it.WaitingOn), Since: it.CreatedAt})
	}
	if pend, ok := base.Pending[x.unit]; ok && sit.Kind != overlay.KindDone {
		r.Pending = &api.PendingRound{Intent: pend.Intent, Reason: pend.Reason, Since: pend.Since, Deadline: pend.Deadline}
		sit = overlay.Pending{Intent: pend.Intent, Reason: pend.Reason}.Over(sit)
	}
	r.Kind = api.Kind(sit.Kind)
	r.Summary = api.Summary{WaitsOn: sit.WaitingOn, Since: sit.Since, Stalled: sit.Stalled, Reason: sit.Reason,
		Closed: sit.Kind == overlay.KindDone, OpenItems: len(r.Items), Line: sit.Reason}
	switch sit.Kind {
	case overlay.KindLoopWaiting, overlay.KindLoopWorking, overlay.KindLoopStopped:
		if x.run != nil {
			r.Question = x.run.Question
		}
	}
	if sit.Kind != reportedKind {
		r.Views = slices.DeleteFunc(r.Views, func(v string) bool { return v == api.ViewNeedsMe })
		if overlay.AsksUser(sit.Kind) {
			r.Views = append(r.Views, api.ViewNeedsMe)
		}
	}
	if i := x.intake; i != nil && i.Waiting() {
		r.Summary.Line = overlay.Briefing(*i)
	}
	r.Stale = x.stale
	r.Actions, r.SpecsVersion = offered(base, r, s.Offers), base.SpecsVersion
	return r, nil
}

// Signals are the link signals the plugin reported of the unit.
func (p *Reported) Signals(_ Base, _ api.Row, in any, _ time.Time) map[string]bool {
	x, ok := in.(*reported)
	if !ok {
		return nil
	}
	out := make(map[string]bool, len(x.snap.Signals))
	for _, g := range x.snap.Signals {
		out[g.Name] = out[g.Name] || g.On
	}
	return out
}

// offered are the intents' actions on r, then the actions the plugin
// offers on the unit.
func offered(base Base, r api.Row, offers []src.Offer) []api.ActionState {
	ids := make([]string, 0, len(offers))
	for _, o := range offers {
		ids = append(ids, o.Action)
	}
	return catalog.WithOffers(catalog.UnitStates(catalog.Facts{Row: r, Tracked: true, Open: !r.Summary.Closed, Asked: base.Asked,
		Intents: base.Intents}), ids)
}

// situation is the unit's situation as its plugin reported it, under the
// run that drives its row and the user's mute.
func (x *reported) situation(base Base, at time.Time) overlay.Situation {
	var sit overlay.Situation
	if rs := x.snap.Situation; rs != nil {
		stalled, _ := value.Resolve(rs.Stalled, at).(bool)
		sit = overlay.Situation{Kind: rs.Kind, WaitingOn: rs.WaitingOn, Since: rs.Since, Reason: overlay.Cut(rs.Reason), Stalled: stalled}
	}
	if x.run != nil && sit.Kind != overlay.KindDone {
		if s, ok := x.run.Turn.Situation(x.sessionWaiting(base)); ok {
			sit = s
		}
	}
	if m := x.mute; m != nil && !x.unmuted && m.Holds(sit, at) {
		sit = m.Muted()
	}
	return sit
}

// sessionWaiting says whether the agents show the run's session waiting;
// a session that could not be read, or agents unknown, cannot show it.
func (x *reported) sessionWaiting(base Base) bool {
	if x.session == nil || x.sessionErr || !base.AgentsOK {
		return false
	}
	return overlay.SessionWaiting(*x.session, base.Agents)
}

func personKeys(ps []src.Person) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Key)
	}
	return out
}

// Card is a reported unit's card: its plugin's, under the overlays its row
// shows. A row whose situation an overlay set says why and on whom in the
// card too.
func (p *Reported) Card(ctx context.Context, row api.Row) (card.Card, error) {
	snaps, err := p.src.Reports.Reported(ctx, []unit.Unit{row.Unit})
	if err != nil {
		return card.Card{}, err
	}
	s, ok := snaps[row.Unit]
	if !ok || s.Card == nil {
		return card.Card{}, fmt.Errorf("reported card: %s has no card", row.Unit)
	}
	c := s.Card
	out := card.Card{Unit: row.Unit, Label: row.Label, Title: c.Title, State: c.State, Outcome: items.Resolution(c.Outcome),
		Why: c.Why, WaitsOn: people(c.WaitsOn), Since: c.Since, Link: c.Link, TurnsLink: c.TurnsLink, StartedByMe: c.StartedByMe,
		ClosurePosted: c.ClosurePosted, Cares: c.Cares, Signals: []card.Signal{}}
	for _, sig := range c.Signals {
		out.Signals = append(out.Signals, card.Signal{Key: sig.Key, Value: sig.Value, Tone: card.Tone(sig.Tone), At: sig.At})
	}
	if h := c.LastHuman; h != nil {
		out.LastHuman = &card.Human{Who: person(h.Who), What: h.What, At: h.At, Link: h.Link}
	}
	if reported := s.Situation; reported == nil || string(row.Kind) != reported.Kind {
		out.Why, out.Since = row.Summary.Reason, row.Summary.Since
		out.WaitsOn = nil
		if row.Summary.WaitsOn == overlay.WaitsOnMe && p.src.Viewer != nil {
			if v := p.src.Viewer(ctx); v.Key != "" {
				out.WaitsOn = []items.Person{v}
			}
		}
	}
	return out, nil
}

func people(ps []src.Person) []items.Person {
	var out []items.Person
	for _, p := range ps {
		out = append(out, person(p))
	}
	return out
}

func person(p src.Person) items.Person {
	return items.Person{Key: p.Key, Name: p.Name, Aliases: slices.Clone(p.Aliases)}
}
