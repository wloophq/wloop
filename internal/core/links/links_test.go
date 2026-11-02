package links_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/ingest/ingesttest"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/links"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	t0     = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	thread = unit.Unit{Provider: ingesttest.Provider, Key: "C1 0001"}
	pull   = unit.Unit{Provider: unit.GitHub, Key: "acme/api#527"}
)

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

type events struct {
	evs   []link.Event
	calls int
}

func (e *events) UnitEvents(_ context.Context, u unit.Unit, since time.Time) ([]link.Event, error) {
	e.calls++
	var out []link.Event
	for _, ev := range e.evs {
		if ev.Unit == u && !ev.At.Before(since) {
			out = append(out, ev)
		}
	}
	return out, nil
}

type ledger struct {
	items   map[unit.Unit][]items.Item
	applied []items.Op
	provs   []items.Provenance
	err     error
}

func (l *ledger) Apply(_ context.Context, prov items.Provenance, ops []items.Op) (coreitems.Result, error) {
	if l.err != nil {
		return coreitems.Result{}, l.err
	}
	var res coreitems.Result
	for _, o := range ops {
		l.applied, l.provs = append(l.applied, o), append(l.provs, prov)
		for i, it := range l.items[o.Unit] {
			if it.ID == o.Item {
				it.State, it.Resolution, it.Evidence = items.Resolved, o.Resolution, *o.Evidence
				l.items[o.Unit][i] = it
				res.Applied = append(res.Applied, coreitems.Applied{Op: o, Item: it, Changed: true})
			}
		}
	}
	return res, nil
}

func (l *ledger) Items(_ context.Context, u unit.Unit) ([]items.Item, error) { return l.items[u], nil }

type fixture struct {
	r      *links.Resolver
	links  *ingesttest.Links
	events *events
	ledger *ledger
	logs   []string
}

func newFixture(t *testing.T, item items.Item, ls ...link.Link) *fixture {
	t.Helper()
	f := &fixture{links: &ingesttest.Links{}, events: &events{}, ledger: &ledger{items: map[unit.Unit][]items.Item{thread: {item}}}}
	for _, l := range ls {
		if err := f.links.Put(context.Background(), l); err != nil {
			t.Fatal(err)
		}
	}
	f.r = &links.Resolver{Links: f.links, Events: map[unit.ProviderID]links.EventReader{unit.GitHub: f.events}, Ledger: f.ledger,
		Clock: clock{t0.Add(48 * time.Hour)}, Log: func(format string, args ...any) { f.logs = append(f.logs, fmt.Sprintf(format, args...)) },
		Codecs: unit.NewRegistry(foldCodec{unit.GitHub})}
	return f
}

func (f *fixture) run(t *testing.T) int {
	t.Helper()
	n, err := f.r.Run(context.Background(), []unit.Unit{pull})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func openItem(assignees ...items.Person) items.Item {
	return items.Item{ID: "i1", Unit: thread, Kind: items.Request, Summary: "review 527", State: items.Open, Assignees: assignees,
		Provenance: items.Extracted, CreatedAt: t0}
}

func reviewLink(kind link.Kind) link.Link {
	return link.Link{From: thread, To: pull, Kind: kind, Item: "i1", Evidence: unit.Anchor{Unit: thread, Ref: "C1/0001"}, Origin: link.Extraction, At: t0}
}

func review(by string, at time.Time) link.Event {
	return link.Event{Unit: pull, Kind: link.EventReview, Actor: "github:" + by, At: at, Anchor: unit.Anchor{Unit: pull, Ref: "review:r" + by, At: at}}
}

func state(kind link.EventKind, at time.Time) link.Event {
	return link.Event{Unit: pull, Kind: kind, Actor: "github:ana", At: at, Anchor: unit.Anchor{Unit: pull, Ref: string(kind), At: at}}
}

var octocat = items.Person{Key: "github:octocat", Name: "Octo"}

func TestReviewByAssigneeResolvesItem(t *testing.T) {
	f := newFixture(t, openItem(octocat), reviewLink(link.Review))
	f.events.evs = []link.Event{review("octocat", t0.Add(time.Hour))}
	if n := f.run(t); n != 1 {
		t.Fatalf("resolved = %d", n)
	}
	if len(f.ledger.applied) != 1 {
		t.Fatalf("ops = %+v", f.ledger.applied)
	}
	op := f.ledger.applied[0]
	if op.Op != items.OpResolve || op.Unit != thread || op.Item != "i1" || op.Resolution != items.Done || f.ledger.provs[0] != items.Derived {
		t.Fatalf("op = %+v, prov %s", op, f.ledger.provs[0])
	}
	if op.Evidence == nil || op.Evidence.Kind != items.EvidenceUnitEvent || op.Evidence.Anchor.Ref != "review:roctocat" ||
		op.Evidence.Anchor.Unit != pull || op.Anchor != op.Evidence.Anchor {
		t.Fatalf("evidence = %+v, anchor %+v", op.Evidence, op.Anchor)
	}
	if n := f.run(t); n != 0 || len(f.ledger.applied) != 1 {
		t.Fatalf("a resolved item resolved again: %d, %+v", n, f.ledger.applied)
	}
}

func TestReviewByAliasResolvesItem(t *testing.T) {
	slackUser := items.Person{Key: "slack:U000AAA", Aliases: []string{"github:OctoCat"}}
	f := newFixture(t, openItem(slackUser), reviewLink(link.Review))
	f.events.evs = []link.Event{review("octocat", t0.Add(time.Hour))}
	if n := f.run(t); n != 1 {
		t.Fatalf("resolved = %d", n)
	}
}

func TestReviewWithoutAssigneeResolvesOnAnyReview(t *testing.T) {
	f := newFixture(t, openItem(), reviewLink(link.Review))
	f.events.evs = []link.Event{review("someone", t0.Add(time.Hour))}
	if n := f.run(t); n != 1 {
		t.Fatalf("resolved = %d", n)
	}
}

func TestReviewByOtherLeavesItemOpen(t *testing.T) {
	f := newFixture(t, openItem(octocat), reviewLink(link.Review))
	f.events.evs = []link.Event{review("someone", t0.Add(time.Hour)), review("octocat", t0.Add(-time.Hour))}
	if n := f.run(t); n != 0 || len(f.ledger.applied) != 0 {
		t.Fatalf("resolved = %d, ops %+v", n, f.ledger.applied)
	}
}

func TestReopenedItemStaysOpen(t *testing.T) {
	it := openItem(octocat)
	it.UserAt = t0.Add(2 * time.Hour)
	f := newFixture(t, it, reviewLink(link.Review))
	f.events.evs = []link.Event{review("octocat", t0.Add(time.Hour))}
	if n := f.run(t); n != 0 || len(f.ledger.applied) != 0 {
		t.Fatalf("resolved = %d, ops %+v", n, f.ledger.applied)
	}
	f.events.evs = append(f.events.evs, review("octocat", t0.Add(3*time.Hour)))
	if n := f.run(t); n != 1 {
		t.Fatalf("a later review did not resolve: %d", n)
	}
}

func TestMergeAndCloseResolveLinks(t *testing.T) {
	cases := []struct {
		name  string
		kind  link.Kind
		event link.EventKind
		want  items.Resolution
	}{
		{"merge link, merged", link.Merge, link.EventMerged, items.Done},
		{"review link, merged", link.Review, link.EventMerged, items.Moot},
		{"merge link, closed", link.Merge, link.EventClosed, items.Moot},
		{"review link, closed", link.Review, link.EventClosed, items.Moot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, openItem(octocat), reviewLink(tc.kind))
			f.events.evs = []link.Event{state(tc.event, t0.Add(time.Hour))}
			if n := f.run(t); n != 1 {
				t.Fatalf("resolved = %d", n)
			}
			op := f.ledger.applied[0]
			if op.Resolution != tc.want || op.Evidence.Anchor.Ref != string(tc.event) {
				t.Fatalf("op = %+v", op)
			}
		})
	}
	f := newFixture(t, openItem(octocat), reviewLink(link.Merge))
	f.events.evs = []link.Event{review("octocat", t0.Add(time.Hour))}
	if n := f.run(t); n != 0 {
		t.Fatalf("a review resolved a merge link: %d", n)
	}
}

func askedAt(at time.Time) items.Item {
	it := openItem(octocat)
	it.Origin = unit.Anchor{Unit: thread, Ref: "C1/0001", At: at}
	return it
}

func TestAnEventBetweenTheAskAndTheLinkSettlesIt(t *testing.T) {
	cases := []struct {
		name string
		kind link.Kind
		evs  []link.Event
		want items.Resolution
	}{
		{"merged", link.Review, []link.Event{state(link.EventMerged, t0.Add(-time.Hour))}, items.Moot},
		{"merged", link.Merge, []link.Event{state(link.EventMerged, t0.Add(-time.Hour))}, items.Done},
		{"closed", link.Merge, []link.Event{state(link.EventClosed, t0.Add(-time.Hour))}, items.Moot},
		{"closed", link.Review, []link.Event{state(link.EventClosed, t0.Add(-time.Hour))}, items.Moot},
		{"reviewed, then merged", link.Review, []link.Event{review("octocat", t0.Add(-90*time.Minute)), state(link.EventMerged, t0.Add(-time.Hour))}, items.Done},
	}
	for _, c := range cases {
		f := newFixture(t, askedAt(t0.Add(-2*time.Hour)), reviewLink(c.kind))
		f.events.evs = c.evs
		if n := f.run(t); n != 1 || f.ledger.applied[0].Resolution != c.want {
			t.Fatalf("%s link, %s before it: resolved = %d, ops %+v", c.kind, c.name, n, f.ledger.applied)
		}
	}
}

func TestAMergeLinkSettlesOnAnEndBeforeTheAsk(t *testing.T) {
	cases := []struct {
		event link.EventKind
		want  items.Resolution
	}{{link.EventMerged, items.Done}, {link.EventClosed, items.Moot}}
	for _, c := range cases {
		f := newFixture(t, askedAt(t0.Add(-30*time.Minute)), reviewLink(link.Merge))
		f.events.evs = []link.Event{state(c.event, t0.Add(-time.Hour))}
		if n := f.run(t); n != 1 || f.ledger.applied[0].Resolution != c.want {
			t.Fatalf("asked to merge a pull request already %s: resolved = %d, ops %+v", c.event, n, f.ledger.applied)
		}
	}
	it := askedAt(t0.Add(-30 * time.Minute))
	it.UserAt = t0
	f := newFixture(t, it, reviewLink(link.Merge))
	f.events.evs = []link.Event{state(link.EventMerged, t0.Add(-time.Hour))}
	if n := f.run(t); n != 0 {
		t.Fatalf("an item you edited after the merge resolved: %+v", f.ledger.applied)
	}
}

func TestAnEndBeforeTheAskLeavesItOpen(t *testing.T) {
	cases := map[string]func(*items.Item){
		"asked after the merge": func(it *items.Item) { it.Origin.At = t0.Add(-30 * time.Minute) },
		"reopened by the user":  func(it *items.Item) { it.UserAt = t0 },
		"reopened since":        func(it *items.Item) { it.ReopenedAt = t0 },
	}
	for name, mod := range cases {
		it := askedAt(t0.Add(-2 * time.Hour))
		mod(&it)
		f := newFixture(t, it, reviewLink(link.Review))
		f.events.evs = []link.Event{state(link.EventMerged, t0.Add(-time.Hour)), review("octocat", t0.Add(-time.Hour))}
		if n := f.run(t); n != 0 {
			t.Fatalf("%s: resolved = %d", name, n)
		}
	}
}

var pull2 = unit.Unit{Provider: unit.GitHub, Key: "acme/api#528"}

func linkTo(to unit.Unit, kind link.Kind) link.Link {
	l := reviewLink(kind)
	l.To = to
	return l
}

func on(u unit.Unit, ev link.Event) link.Event {
	ev.Unit, ev.Anchor.Unit = u, u
	return ev
}

func TestAnItemLinkedToSeveralPullRequestsSettlesOnceEveryOneDoes(t *testing.T) {
	f := newFixture(t, openItem(octocat), linkTo(pull, link.Merge), linkTo(pull2, link.Merge))
	f.events.evs = []link.Event{state(link.EventMerged, t0.Add(time.Hour))}
	if n, err := f.r.Run(context.Background(), []unit.Unit{pull, pull2}); err != nil || n != 0 {
		t.Fatalf("one of two merged: resolved = %d, %v, ops %+v", n, err, f.ledger.applied)
	}
	f.events.evs = append(f.events.evs, on(pull2, state(link.EventClosed, t0.Add(2*time.Hour))))
	if n := f.run(t); n != 1 {
		t.Fatalf("both ended: resolved = %d", n)
	}
	op := f.ledger.applied[0]
	if op.Resolution != items.Done || op.Evidence.Anchor.Unit != pull2 || op.Evidence.Anchor.Ref != "closed" {
		t.Fatalf("one merged and one closed: op %+v, evidence %+v", op, op.Evidence)
	}

	f = newFixture(t, openItem(octocat), linkTo(pull, link.Merge), linkTo(pull2, link.Review))
	f.events.evs = []link.Event{state(link.EventClosed, t0.Add(time.Hour)), on(pull2, state(link.EventClosed, t0.Add(2*time.Hour)))}
	if n := f.run(t); n != 1 || f.ledger.applied[0].Resolution != items.Moot {
		t.Fatalf("both closed: resolved = %d, ops %+v", n, f.ledger.applied)
	}
}

type refresher struct {
	// read is what a read of the target adds to its events; nil reads nothing.
	read  func()
	err   error
	calls []unit.Unit
}

func (r *refresher) Refresh(_ context.Context, u unit.Unit) (bool, error) {
	r.calls = append(r.calls, u)
	if r.err != nil || r.read == nil {
		return false, r.err
	}
	r.read()
	return true, nil
}

func TestAPendingTargetIsReadAgainAndSettlesInTheSameRun(t *testing.T) {
	f := newFixture(t, openItem(octocat), reviewLink(link.Merge))
	rf := &refresher{read: func() { f.events.evs = []link.Event{state(link.EventMerged, t0.Add(time.Hour))} }}
	f.r.Refresh = map[unit.ProviderID]links.Refresher{unit.GitHub: rf}
	if n := f.run(t); n != 1 || len(rf.calls) != 1 || rf.calls[0] != pull {
		t.Fatalf("resolved = %d, reads %v", n, rf.calls)
	}
	if n := f.run(t); n != 0 || len(rf.calls) != 1 {
		t.Fatalf("a settled item read its target again: %v", rf.calls)
	}
}

func TestAFailedReadWaitsBeforeTheNext(t *testing.T) {
	cases := []struct {
		name string
		err  error
		wait time.Duration
	}{
		{"unavailable", errors.New("rate limited"), links.RetryAfter},
		{"not found", fmt.Errorf("%w: gone", port.ErrNotFound), links.RetryNotFoundAfter},
	}
	for _, c := range cases {
		f := newFixture(t, openItem(octocat), reviewLink(link.Merge))
		rf := &refresher{err: c.err}
		f.r.Refresh = map[unit.ProviderID]links.Refresher{unit.GitHub: rf}
		now := t0.Add(48 * time.Hour)
		f.r.Clock = clock{now}
		if n, err := f.r.Run(context.Background(), []unit.Unit{pull}); n != 0 || err != nil {
			t.Fatalf("%s: a failed read failed the run: %d, %v", c.name, n, err)
		}
		f.run(t)
		f.r.Clock = clock{now.Add(c.wait - time.Second)}
		f.run(t)
		if len(rf.calls) != 1 {
			t.Fatalf("%s: read again before %v: %d reads", c.name, c.wait, len(rf.calls))
		}
		f.r.Clock = clock{now.Add(c.wait)}
		f.run(t)
		if len(rf.calls) != 2 {
			t.Fatalf("%s: not read again after %v: %d reads", c.name, c.wait, len(rf.calls))
		}
		if len(f.logs) == 0 || !strings.Contains(f.logs[0], "cannot be read: ") {
			t.Fatalf("%s: logs = %q", c.name, f.logs)
		}
	}
}

func TestFirstSettlingEventWins(t *testing.T) {
	f := newFixture(t, openItem(octocat), reviewLink(link.Review))
	f.events.evs = []link.Event{state(link.EventClosed, t0.Add(2*time.Hour)), review("octocat", t0.Add(time.Hour))}
	f.run(t)
	if op := f.ledger.applied[0]; op.Resolution != items.Done {
		t.Fatalf("op = %+v", op)
	}
}

func TestResolverSkipsWhatCannotResolve(t *testing.T) {
	later := []link.Event{review("octocat", t0.Add(time.Hour)), state(link.EventMerged, t0.Add(2*time.Hour))}
	mention := reviewLink(link.Mention)
	unitOnly := reviewLink(link.Review)
	unitOnly.Item = ""
	resolved := openItem(octocat)
	resolved.State, resolved.Resolution = items.Resolved, items.Done
	unknown := reviewLink(link.Review)
	unknown.Item = "i9"
	cases := []struct {
		name string
		item items.Item
		link link.Link
	}{
		{"mention", openItem(octocat), mention},
		{"no item", openItem(octocat), unitOnly},
		{"already resolved", resolved, reviewLink(link.Review)},
		{"unknown item", openItem(octocat), unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.item, tc.link)
			f.events.evs = later
			if n := f.run(t); n != 0 || len(f.ledger.applied) != 0 {
				t.Fatalf("resolved = %d, ops %+v", n, f.ledger.applied)
			}
		})
	}
	t.Run("no event reader", func(t *testing.T) {
		f := newFixture(t, openItem(octocat), reviewLink(link.Review))
		f.r.Events = nil
		if n := f.run(t); n != 0 || len(f.ledger.applied) != 0 {
			t.Fatalf("resolved = %d", n)
		}
		f.run(t)
		if len(f.logs) != 1 || !strings.Contains(f.logs[0], "link target "+pull.String()+" has no events") {
			t.Fatalf("logs = %q", f.logs)
		}
		f.r.Clock = clock{t0.Add(96 * time.Hour)}
		f.run(t)
		if len(f.logs) != 2 {
			t.Fatalf("a day later, logs = %q", f.logs)
		}
	})
	t.Run("no settling event is the waiting state and logs nothing", func(t *testing.T) {
		f := newFixture(t, openItem(octocat), reviewLink(link.Review))
		if n := f.run(t); n != 0 || len(f.ledger.applied) != 0 {
			t.Fatalf("resolved = %d", n)
		}
		f.events.evs = []link.Event{review("someone-else", t0.Add(time.Hour))}
		if n := f.run(t); n != 0 || len(f.ledger.applied) != 0 {
			t.Fatalf("resolved = %d", n)
		}
		if len(f.logs) != 0 {
			t.Fatalf("logs = %q", f.logs)
		}
	})
	t.Run("disabled plugin", func(t *testing.T) {
		f := newFixture(t, openItem(octocat), reviewLink(link.Review))
		f.events.evs = later
		f.ledger.err = &coreitems.DisabledError{Provider: ingesttest.Provider}
		if n, err := f.r.Run(context.Background(), []unit.Unit{pull}); n != 0 || err != nil {
			t.Fatalf("resolved = %d, %v", n, err)
		}
	})
	t.Run("nothing links to the target", func(t *testing.T) {
		f := newFixture(t, openItem(octocat))
		f.events.evs = later
		if n := f.run(t); n != 0 || f.events.calls != 0 {
			t.Fatalf("resolved = %d, event reads %d", n, f.events.calls)
		}
	})
}

// foldCodec compares its provider's keys without case, as GitHub's does.
type foldCodec struct{ id unit.ProviderID }

func (c foldCodec) Provider() unit.ProviderID { return c.id }
func (foldCodec) Valid(string) error          { return nil }
func (foldCodec) Equal(a, b string) bool      { return strings.EqualFold(a, b) }

func TestSettlesWhatTheTargetAlreadyShows(t *testing.T) {
	asked := askedAt(t0.Add(-2 * time.Hour))
	f := newFixture(t, asked)
	f.events.evs = []link.Event{state(link.EventMerged, t0.Add(-time.Hour))}
	op, ok, err := f.r.Settles(context.Background(), []link.Link{reviewLink(link.Merge)}, asked)
	if err != nil || !ok || op.Op != items.OpResolve || op.Unit != thread || op.Item != "i1" || op.Resolution != items.Done ||
		op.Evidence == nil || op.Evidence.Anchor.Ref != "merged" {
		t.Fatalf("op %+v ok %v err %v", op, ok, err)
	}
	if _, ok, _ := f.r.Settles(context.Background(), []link.Link{reviewLink(link.Mention)}, asked); ok {
		t.Fatal("a mention settled its item")
	}
	f.events.evs = []link.Event{review("octocat", t0.Add(-3*time.Hour))}
	if _, ok, _ := f.r.Settles(context.Background(), []link.Link{reviewLink(link.Review)}, asked); ok {
		t.Fatal("a review before the ask settled it")
	}
	f.events.evs = []link.Event{state(link.EventMerged, t0.Add(-time.Hour))}
	if _, ok, _ := f.r.Settles(context.Background(), []link.Link{linkTo(pull, link.Merge), linkTo(pull2, link.Merge)}, asked); ok {
		t.Fatal("one of two merged settled the item")
	}
	if len(f.ledger.applied) != 0 {
		t.Fatalf("Settles applied %+v", f.ledger.applied)
	}
}
