package daemon

import (
	"context"
	"strings"
	"sync"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

const (
	viewerTTL   = 10 * time.Minute
	viewerRetry = time.Minute
)

type viewerCache struct {
	mu       sync.Mutex
	id       src.Identity
	at       time.Time
	gen      port.Generation
	err      error
	failedAt time.Time
}

// cachedLogin never asks the source, so health stays instant on a cold
// cache.
func (s *Server) cachedLogin() string {
	s.viewer.mu.Lock()
	defer s.viewer.mu.Unlock()
	return s.login(s.viewer.id)
}

// login is the viewer's login in the forge from id.
func (s *Server) login(id src.Identity) string { return readmodel.Login(s.deps.Forge, id) }

// viewerInfo is who the viewer is in the forge, as its plugin's Identity
// says. It serves a stale identity when a refresh fails, so a source's
// hiccup does not blank the cockpit, and waits viewerRetry before asking
// again. The identity lasts until a TopicViewer note, or viewerTTL without
// a change log.
func (s *Server) viewerInfo(ctx context.Context) (src.Identity, error) {
	c := &s.viewer
	now := s.deps.Now()
	c.mu.Lock()
	fresh := !c.at.IsZero() && s.viewerFresh(c, now)
	failedRecently := !c.failedAt.IsZero() && now.Sub(c.failedAt) < viewerRetry
	if s.deps.Identity == nil || fresh || failedRecently {
		id, err := c.id, c.err
		c.mu.Unlock()
		return id, err
	}
	c.mu.Unlock()

	gen := s.changeGen()
	id, err := s.deps.Identity(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.failedAt = now
		if c.at.IsZero() {
			c.err = err
			return src.Identity{}, err
		}
		s.logf("viewer: %v", err)
		return c.id, nil
	}
	c.id, c.at, c.gen, c.err, c.failedAt = id, now, gen, nil, time.Time{}
	return id, nil
}

func (s *Server) viewerFresh(c *viewerCache, now time.Time) bool {
	if s.deps.Changes == nil {
		return now.Sub(c.at) < viewerTTL
	}
	return s.deps.Changes.Since(c.gen, port.TopicViewer).Empty()
}

// turnFacts reads every book once and resolves a started turn against the
// agents' last known states: whether its session waits is a signal, never
// the turn's state.
func (s *Server) turnFacts(ctx context.Context, reg *dintent.Registry) (turnFactMap, error) {
	if s.deps.Turns == nil {
		return nil, nil
	}
	books, err := s.deps.Turns.Store.Books(ctx)
	if err != nil {
		return nil, err
	}
	out := prrow.TurnFacts(books, reg)
	if !prrow.Started(out) {
		return out, nil
	}
	var agents []turns.AgentObservation
	ok := false
	if s.deps.Agents != nil {
		agents, ok = s.deps.Agents.Agents()
	}
	prrow.Waiting(out, agents, ok, func(handle string) (turn.Session, bool, error) {
		return s.deps.Turns.Registry.Session(ctx, handle)
	}, s.logf)
	return out, nil
}

// rows are the read model's rows of view.
func (s *Server) rows(ctx context.Context, view string) ([]api.Row, error) {
	page, err := s.readModel().Rows(ctx, view, api.Since{})
	return page.Rows, err
}

// readModel is built at the first read, once the deps are settled.
func (s *Server) readModel() *readmodel.Model {
	s.modelOnce.Do(func() { s.model = s.newModel() })
	return s.model
}

// expireMutes clears every "mute until state change" whose state changed,
// over every row the read model lists, so the mute does not come back when
// the situation returns.
func (s *Server) expireMutes(ctx context.Context) error {
	k, err := s.readModel().Current(ctx)
	if err != nil {
		return err
	}
	return s.expireLapsedMutes(ctx, k)
}

func (s *Server) expireLapsedMutes(ctx context.Context, k readmodel.Kept) error {
	for _, o := range prrow.LapsedMutes(k) {
		if _, _, err := s.expireMute(ctx, o.PR.Unit(), o); err != nil {
			return err
		}
	}
	return nil
}

// expireMute clears the stored "mute until state change" of ref when it is
// still the mute o holds, and returns the override as it stands.
func (s *Server) expireMute(ctx context.Context, u unit.Unit, o dinv.Override) (cleared bool, _ dinv.Override, err error) {
	s.overrideMu.Lock()
	cleared, err = s.deps.Overrides.ClearMute(ctx, u, o.MutedKind, o.MutedWaitingOn)
	if err == nil {
		o, err = s.deps.Overrides.Override(ctx, u)
	}
	s.overrideMu.Unlock()
	if err != nil {
		return false, o, err
	}
	if cleared {
		if err := s.recordEvent(ctx, u, dinv.KindUnmuted, "state changed"); err != nil {
			return false, o, err
		}
	}
	return cleared, o, nil
}

func (s *Server) detail(ctx context.Context, u unit.Unit) (api.Detail, bool, error) {
	var (
		row  api.Row
		pr   dinv.PR
		evs  []dinv.Event
		book *turn.Book
	)
	rd, ok, err := s.readRow(ctx, u)
	if err != nil {
		return api.Detail{}, false, err
	}
	if ok {
		row, evs = rd.Row, prrow.EventsOf(rd.In)
		pr, _ = prrow.PROf(rd.In)
		if b, has := prrow.BookOf(rd.In); has {
			book = &b
		}
	} else {
		st, found, err := s.storedRead(ctx, u, catalog.Asked{}, false)
		if err != nil || !found {
			return api.Detail{}, false, err
		}
		row, pr, evs, book = st.row, st.pr, st.evs, st.book
	}
	timeline := []dinv.Event{}
	for _, e := range evs {
		if !dinv.Bookkeeping(e.Kind) {
			timeline = append(timeline, e)
		}
	}
	var view *brief.View
	if s.deps.BriefViews != nil && isPullRequest(u) {
		views, err := s.deps.BriefViews.Views(ctx, pr)
		if err != nil {
			s.logf("detail %s: brief: %v", u.Label(), err)
		} else if err = s.deps.BriefViews.RefreshIfStale(ctx, pr); err != nil {
			s.logf("detail %s: brief refresh: %v", u.Label(), err)
		}
		view = views[pr.Ref]
	}
	var layers []string
	for _, l := range profile.Chain(s.deps.Cfg) {
		layers = append(layers, l.Name)
	}
	var tv *api.TurnView
	if book != nil {
		tv = wireTurnView(*book, s.loaded().Registry.Answerable())
	}
	return api.Detail{Row: row, Events: wireEvents(timeline), Brief: wireBriefView(view), ProfileChain: strings.Join(layers, " + "), Turn: tv}, true, nil
}

const (
	activityDefault = 200
	activityMax     = 1000
)

func (s *Server) activity(ctx context.Context, limit int) ([]api.ActivityItem, error) {
	evs, err := s.deps.Events.RecentActivity(ctx, limit)
	if err != nil {
		return nil, err
	}
	viewer := s.cachedLogin()
	out := []api.ActivityItem{}
	for _, e := range evs {
		ev := e.Event
		var label string
		if !ev.Unit.IsZero() {
			label = ev.Unit.Label()
		}
		out = append(out, api.ActivityItem{Event: api.Event{Unit: ev.Unit, Kind: ev.Kind, Key: ev.Key, Actor: ev.Actor, At: ev.At, Data: ev.Data}, Label: label, Title: e.Title, URL: e.URL, Author: e.Author, State: e.State,
			Mine: e.Mine || viewer != "" && strings.EqualFold(e.Author, viewer)})
	}
	return out, nil
}

// newModel is the read model over the daemon's stores, gate and caches.
func (s *Server) newModel() *readmodel.Model {
	d := s.deps
	var changes port.Changes
	if d.Changes != nil {
		changes = d.Changes
	}
	rd := readmodel.RowDeps{Activity: d.Activity, Intake: s.rowIntake, Settings: s.rowSettings, Logf: s.logf}
	if d.Turns != nil {
		rd.Books, rd.Sessions = d.Turns.Store, d.Turns.Registry
	}
	if s.intakes != nil {
		rd.Intakes = s.rowIntakes
	}
	pending := s.pending()
	providers := make([]readmodel.Provider, 0, len(d.RowProviders))
	for _, rows := range d.RowProviders {
		providers = append(providers, rows(rd))
	}
	m := readmodel.New(changes, d.Snapshot, pending, d.Agents, modelSettings{s}, providers...)
	m.UseClock(func() time.Time { return s.deps.Now() })
	m.UseLog(s.logf)
	m.UseKeeper(s.keepRows)
	return m
}

// rowIntake is a row's view of an open intake, from the brief record the
// rebuild read, as of the minute's start like the rest of the row.
func (s *Server) rowIntake(i intake.Intake, b intake.BriefState, attempt func() (int, error)) api.Intake {
	return s.intakeView(i, b, s.deps.Now().Truncate(time.Minute), attempt)
}

func (s *Server) rowSettings() readmodel.RowSettings {
	return readmodel.RowSettings{Cfg: s.deps.Cfg, Loaded: s.loaded(), GateOn: s.gateOn(), Forceable: s.forceableOf}
}

// gateOn says whether automatic rounds wait in a window the user may act
// in: the plan's.
func (s *Server) gateOn() bool {
	return s.deps.Plan != nil
}

// pending are the plan's pending automatic rounds; none without a plan.
func (s *Server) pending() readmodel.Pending {
	if s.deps.Pending != nil {
		return s.deps.Pending
	}
	return noPending{}
}

type noPending struct{}

func (noPending) Pending(context.Context) (map[unit.Unit]round.Pending, error) { return nil, nil }

type modelSettings struct{ s *Server }

func (m modelSettings) Settings(ctx context.Context) (readmodel.Base, error) {
	id, err := m.s.viewerInfo(ctx)
	if err != nil {
		return readmodel.Base{}, err
	}
	l := m.s.loaded()
	off := m.s.disabledPlugins(l)
	b := readmodel.Base{Specs: catalog.SpecsFor(l, off), SpecsVersion: catalog.VersionFor(l, off), Intents: l}
	b.Identify(m.s.deps.Forge, id)
	return b, nil
}

// servedSpecs are the specs of the loaded intents without the disabled
// plugins' actions.
func (s *Server) servedSpecs() []api.ActionSpec {
	l := s.loaded()
	return catalog.SpecsFor(l, s.disabledPlugins(l))
}
