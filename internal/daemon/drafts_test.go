package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wloophq/wloop/internal/domain/items"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/keys"
)

var slackUnit = unit.Unit{Provider: "slack", Key: "C000AAA 1700000000.000100"}

// fakeFx publishes into a list and verifies from found.
type fakeFx struct {
	mu     sync.Mutex
	calls  []string
	fail   error
	refuse error
	found  bool
	posted chan draft.ID
	// whereErr fails Where; whereHangs makes it wait for its deadline; wheres counts its calls.
	whereErr   error
	whereHangs bool
	wheres     int
}

func (f *fakeFx) Where(ctx context.Context, d draft.Draft) (string, error) {
	f.mu.Lock()
	f.wheres++
	hangs, err := f.whereHangs, f.whereErr
	f.mu.Unlock()
	if hangs {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if err != nil {
		return "", err
	}
	return "#general in the thread " + strconv.Quote(d.Unit.Key), nil
}

func (f *fakeFx) whereCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.wheres
}

func (f *fakeFx) Publish(_ context.Context, d draft.Draft) (draft.Evidence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("publish %d", d.ID))
	if f.posted != nil {
		f.posted <- d.ID
	}
	if f.fail != nil {
		return draft.Evidence{}, f.fail
	}
	return draft.Evidence{Ref: fmt.Sprint(d.ID), Permalink: fmt.Sprintf("https://example.test/p/%d", d.ID)}, nil
}

func (f *fakeFx) Verify(_ context.Context, d draft.Draft, _ time.Time) (draft.Evidence, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("verify %d", d.ID))
	return draft.Evidence{Ref: fmt.Sprint(d.ID)}, f.found, nil
}

func (f *fakeFx) Validate(context.Context, draft.Draft) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refuse
}

func (f *fakeFx) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// moveClock is a clock tests move.
type moveClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *moveClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *moveClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

type outboxRig struct {
	*entriesRig
	fx    *fakeFx
	o     *outbox.Service
	clock *moveClock
}

// newOutboxRig serves the Outbox over the entries rig: github publishes through fx, slack is a plugin with no
// publisher, and off names the plugins that are disabled.
func newOutboxRig(t *testing.T, off ...unit.ProviderID) *outboxRig {
	t.Helper()
	r := &outboxRig{fx: &fakeFx{}, clock: &moveClock{now: entriesNow}}
	r.entriesRig = newEntriesRig(t, func(d *Deps) {
		if len(off) > 0 {
			d.PluginEnabled = func(p unit.ProviderID) bool { return !slices.Contains(off, p) }
		}
	})
	r.o = &outbox.Service{Store: r.db.Drafts(), Clock: r.clock, Effects: func(p unit.ProviderID) (port.Effects, error) {
		if p == unit.GitHub && !slices.Contains(off, p) {
			return r.fx, nil
		}
		return nil, fmt.Errorf("%w: %s", outbox.ErrDisabled, p)
	}}
	r.s.deps.Outbox = r.o
	r.s.registerOutbox()
	return r
}

func (r *outboxRig) propose(t *testing.T, u unit.Unit, text string, mode draft.ApprovalMode) draft.Draft {
	t.Helper()
	a := draft.Approval{Mode: mode}
	if mode == draft.Window {
		a.Window = time.Minute
	}
	d, err := r.o.Propose(context.Background(), outbox.Proposal{Unit: u, Kind: draft.Reply, Text: text,
		Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "test", Key: text}, Approval: &a})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// stored puts d in the store as is, for states no service call reaches here.
func (r *outboxRig) stored(t *testing.T, d draft.Draft) draft.Draft {
	t.Helper()
	if d.Kind == "" {
		d.Kind = draft.Reply
	}
	d.Original = d.Text
	d.CreatedAt, d.UpdatedAt = r.clock.Now(), r.clock.Now()
	got, err := r.db.Drafts().Insert(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (r *outboxRig) act(t *testing.T, id draft.ID, action, body string) (int, api.Draft, string) {
	t.Helper()
	rec := post(t, r.s, api.DraftPath(int64(id), action), body)
	var d api.Draft
	if rec.Code == 200 {
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, d, rec.Body.String()
}

func TestDraftsListByState(t *testing.T) {
	r := newOutboxRig(t)
	first := r.propose(t, r7u, "first", draft.Always)
	r.clock.set(entriesNow.Add(time.Minute))
	second := r.propose(t, r7u, "second\nmore", draft.Always)
	r.propose(t, r7u, "watched", draft.Observe)
	var res api.DraftsResponse
	if code := get(t, r.s, api.Drafts+"?state=proposed", &res); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	var ids []int64
	for _, d := range res.Drafts {
		ids = append(ids, d.ID)
		if d.State != "proposed" || d.Unit != "github/acme/api#7" || d.Approval.Mode != "always" || len(d.Actions) != 4 {
			t.Errorf("draft %+v", d)
		}
	}
	if !slices.Equal(ids, []int64{int64(second.ID), int64(first.ID)}) {
		t.Fatalf("ids %v, want newest first", ids)
	}
	if code := get(t, r.s, api.Drafts+"?state=nope", nil); code != 400 {
		t.Fatalf("unknown state: %d", code)
	}
	var one api.Draft
	if code := get(t, r.s, api.DraftPath(int64(second.ID)), &one); code != 200 || one.Text != "second\nmore" {
		t.Fatalf("GET one: %d %+v", code, one)
	}
}

func TestApproveWithEditedText(t *testing.T) {
	r := newOutboxRig(t)
	d := r.propose(t, r7u, "Thanks, merging", draft.Always)
	code, got, body := r.act(t, d.ID, "approve", `{"rev":0,"text":"Thanks! Merging now."}`)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if got.State != "approved" || got.Text != "Thanks! Merging now." || got.Original != "Thanks, merging" || !got.Edited || got.Would.Outcome != "edited" {
		t.Fatalf("approved %+v", got)
	}
	if calls := r.fx.seen(); len(calls) != 0 {
		t.Fatalf("the route published: %v", calls)
	}
}

func TestApproveAnswers409WhenPluginDisabled(t *testing.T) {
	r := newOutboxRig(t, "slack")
	d := r.stored(t, draft.Draft{Unit: slackUnit, Text: "hi", State: draft.Proposed})
	code, _, body := r.act(t, d.ID, "approve", `{}`)
	var e struct{ Error, Plugin string }
	json.Unmarshal([]byte(body), &e)
	if code != 409 || e.Plugin != "slack" || e.Error != "slack is disabled" {
		t.Fatalf("%d %s", code, body)
	}
	if got, _ := r.o.Store.Get(context.Background(), d.ID); got.State != draft.Proposed {
		t.Fatalf("state %s", got.State)
	}
}

func TestDraftActionStatuses(t *testing.T) {
	r := newOutboxRig(t)
	d := r.propose(t, r7u, "hello", draft.Always)
	cases := []struct {
		name   string
		id     draft.ID
		action string
		body   string
		code   int
		has    string
	}{
		{"retry of a proposed draft", d.ID, "retry", `{"rev":0}`, 409, "proposed"},
		{"unknown id", 999, "approve", `{}`, 404, "no such draft"},
		{"empty text", d.ID, "edit", `{"text":"  "}`, 400, "empty text"},
		{"edit without text", d.ID, "edit", `{}`, 400, "text"},
		{"unknown action", d.ID, "post", `{}`, 400, "unknown draft action"},
		{"bad body", d.ID, "approve", `{`, 400, "invalid body"},
	}
	for _, c := range cases {
		code, _, body := r.act(t, c.id, c.action, c.body)
		if code != c.code || !strings.Contains(body, c.has) {
			t.Errorf("%s: %d %s", c.name, code, body)
		}
	}
	if code, _, body := r.act(t, d.ID, "discard", `{"reason":"stale"}`); code != 200 || !strings.Contains(body, `"reason":"stale"`) {
		t.Fatalf("discard: %d %s", code, body)
	}
	if code, _, body := r.act(t, d.ID, "approve", `{"rev":1,"text":"x"}`); code != 409 || !strings.Contains(body, "discarded") {
		t.Fatalf("approve of a discarded draft: %d %s", code, body)
	}
	if code := get(t, r.s, api.Drafts+"/x", nil); code != 404 {
		t.Fatalf("GET a bad id: %d", code)
	}
}

func TestEnabledPluginWithoutPublisherAnswers409(t *testing.T) {
	r := newOutboxRig(t)
	d := r.stored(t, draft.Draft{Unit: slackUnit, Text: "hi", State: draft.Proposed})
	code, _, body := r.act(t, d.ID, "approve", `{"rev":0}`)
	var e struct{ Error, Plugin string }
	json.Unmarshal([]byte(body), &e)
	if code != 409 || e.Plugin != "slack" {
		t.Fatalf("%d %s", code, body)
	}
}

// draftClock records the sender's waits and fires them when the test says.
type draftClock struct {
	waits chan time.Duration
	fire  chan time.Time
}

func (c draftClock) Now() time.Time { return entriesNow }

func (c draftClock) After(d time.Duration) <-chan time.Time {
	c.waits <- d
	return c.fire
}

func (r *outboxRig) startSender(t *testing.T) draftClock {
	t.Helper()
	clock := draftClock{waits: make(chan time.Duration, 16), fire: make(chan time.Time)}
	r.s.draftClock = clock
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.s.sendDrafts(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return clock
}

func (c draftClock) next(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-c.waits:
		return d
	case <-time.After(10 * time.Second):
		t.Fatal("the sender never waited")
		return 0
	}
}

func TestApprovedDraftPublishesOnWake(t *testing.T) {
	r := newOutboxRig(t)
	r.fx.posted = make(chan draft.ID, 4)
	d := r.propose(t, r7u, "ship it", draft.Always)
	clock := r.startSender(t)
	if w := clock.next(t); w != draftWatchdog {
		t.Fatalf("idle wait %v", w)
	}
	if code, _, body := r.act(t, d.ID, "approve", `{"rev":0}`); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	select {
	case id := <-r.fx.posted:
		if id != d.ID {
			t.Fatalf("published %d", id)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the approval did not wake the sender")
	}
	waitUntil(t, "the draft is sent", func() bool {
		got, _ := r.o.Store.Get(context.Background(), d.ID)
		return got.State == draft.Sent
	})
	if calls := r.fx.seen(); !slices.Equal(calls, []string{fmt.Sprintf("publish %d", d.ID)}) {
		t.Fatalf("calls %v", calls)
	}
}

func TestSenderWakesAtTheDeadline(t *testing.T) {
	r := newOutboxRig(t)
	d := r.propose(t, r7u, "auto", draft.Window)
	clock := r.startSender(t)
	if w := clock.next(t); w != time.Minute {
		t.Fatalf("waits %v, want the window", w)
	}
	if len(r.fx.seen()) != 0 {
		t.Fatal("published before the deadline")
	}
	r.clock.set(entriesNow.Add(time.Minute))
	clock.fire <- entriesNow.Add(time.Minute)
	waitUntil(t, "the window draft is sent", func() bool {
		got, _ := r.o.Store.Get(context.Background(), d.ID)
		return got.State == draft.Sent && got.Would.Outcome == draft.OutcomeAuto
	})
	if calls := r.fx.seen(); !slices.Equal(calls, []string{fmt.Sprintf("publish %d", d.ID)}) {
		t.Fatalf("calls %v", calls)
	}
}

func TestSenderClampsAPastDeadline(t *testing.T) {
	r := newOutboxRig(t)
	d := r.propose(t, r7u, "auto", draft.Window)
	r.fx.refuse = errors.New("unknown person")
	r.clock.set(entriesNow.Add(time.Hour))
	clock := r.startSender(t)
	for range 2 {
		if w := clock.next(t); w != draftDueRetry {
			t.Fatalf("waits %v on a refused due draft, want %v", w, draftDueRetry)
		}
		go func() { clock.fire <- time.Time{} }()
	}
	if got, _ := r.o.Store.Get(context.Background(), d.ID); got.State != draft.Proposed {
		t.Fatalf("state %s", got.State)
	}
}

func TestSenderRecoversBeforePublishing(t *testing.T) {
	r := newOutboxRig(t)
	r.fx.found = true
	stuck := r.stored(t, draft.Draft{Unit: r7u, Text: "was sending", State: draft.Sending, SendingAt: entriesNow})
	ready := r.stored(t, draft.Draft{Unit: r7u, Text: "approved", State: draft.Approved})
	clock := r.startSender(t)
	clock.next(t)
	want := []string{fmt.Sprintf("verify %d", stuck.ID), fmt.Sprintf("publish %d", ready.ID)}
	if calls := r.fx.seen(); !slices.Equal(calls, want) {
		t.Fatalf("calls %v, want %v", calls, want)
	}
	if got, _ := r.o.Store.Get(context.Background(), stuck.ID); got.State != draft.Sent {
		t.Fatalf("the recovered draft is %s", got.State)
	}
}

func TestOutboxViewListsOpenDrafts(t *testing.T) {
	r := newOutboxRig(t)
	a := r.propose(t, r7u, "one", draft.Always)
	b := r.propose(t, r7u, "two\nsecond line", draft.Always)
	sent := r.propose(t, r7u, "three", draft.Always)
	if _, err := r.o.Approve(context.Background(), sent.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.o.Send(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := r.page(t, "view=outbox")
	if got, want := entryIDs(p.Entries), []string{fmt.Sprintf("draft:%d", b.ID), fmt.Sprintf("draft:%d", a.ID)}; !slices.Equal(got, want) {
		t.Fatalf("entries %v, want %v", got, want)
	}
	e := p.Entries[0]
	if e.Kind != "draft" || e.Draft == nil || e.Draft.ID != int64(b.ID) || e.Title != "reply on acme/api#7: two" || !slices.Equal(e.Units, []unit.Unit{r7u}) ||
		!slices.Equal(e.UnitLabels, []string{"acme/api#7"}) {
		t.Fatalf("entry %+v", e)
	}
	if p.Counters.Views["outbox"] != 2 || p.Counters.Badge < 2 {
		t.Fatalf("counters %+v", p.Counters)
	}
	var c api.Counters
	if get(t, r.s, api.CountersPath, &c); c.Views["outbox"] != 2 {
		t.Fatalf("counters route %+v", c)
	}
	var detail api.EntryView
	if code := get(t, r.s, api.EntryPath(e.ID), &detail); code != 200 || detail.Draft == nil || detail.Draft.Text != "two\nsecond line" {
		t.Fatalf("detail %d %+v", code, detail)
	}
	verbs := map[string]bool{}
	for _, v := range detail.Verbs {
		verbs[v.Verb] = v.Enabled
	}
	if !verbs["approve"] || !verbs["edit"] || !verbs["dismiss"] || verbs["retry"] || verbs["next"] || verbs["done"] {
		t.Fatalf("verbs %+v", detail.Verbs)
	}
	if p := r.page(t, "view=outbox&context=@reply"); len(p.Entries) != 0 {
		t.Fatalf("a context filter keeps %v", entryIDs(p.Entries))
	}
}

func TestSendingDraftStaysVisibleBeforeRecovery(t *testing.T) {
	r := newOutboxRig(t)
	d := r.stored(t, draft.Draft{Unit: r7u, Text: "in flight", State: draft.Sending, SendingAt: entriesNow})
	p := r.page(t, "view=outbox")
	if !slices.Equal(entryIDs(p.Entries), []string{fmt.Sprintf("draft:%d", d.ID)}) || p.Counters.Views["outbox"] != 1 {
		t.Fatalf("entries %v counters %+v", entryIDs(p.Entries), p.Counters)
	}
	for _, v := range p.Entries[0].Verbs {
		if v.Enabled {
			t.Errorf("%s enabled on a sending draft", v.Verb)
		}
	}
}

func (r *outboxRig) verb(t *testing.T, id draft.ID, verb, args string) (int, string) {
	t.Helper()
	rec := post(t, r.s, api.EntryPath(fmt.Sprintf("draft:%d", id), verb), `{"source":"cockpit","args":`+args+`}`)
	return rec.Code, rec.Body.String()
}

func TestOutboxVerbsActOnTheDraft(t *testing.T) {
	r := newOutboxRig(t)
	ctx := context.Background()
	a := r.propose(t, r7u, "approve me", draft.Always)
	if code, body := r.verb(t, a.ID, "approve", `{"rev":"0","text":"approved text"}`); code != 200 || !strings.Contains(body, `"state":"next"`) {
		t.Fatalf("approve: %d %s", code, body)
	}
	if got, _ := r.o.Store.Get(ctx, a.ID); got.State != draft.Approved || got.Text != "approved text" {
		t.Fatalf("approved %+v", got)
	}
	if code, body := r.verb(t, a.ID, "approve", `{"rev":"1","text":"again"}`); code != 409 {
		t.Fatalf("approve twice: %d %s", code, body)
	}
	b := r.propose(t, r7u, "drop me", draft.Always)
	if code, body := r.verb(t, b.ID, "dismiss", `{}`); code != 200 {
		t.Fatalf("dismiss: %d %s", code, body)
	}
	if got, _ := r.o.Store.Get(ctx, b.ID); got.State != draft.Discarded || got.Reason != "user" {
		t.Fatalf("dismissed %+v", got)
	}
	c := r.stored(t, draft.Draft{Unit: r7u, Text: "maybe out", State: draft.Failed, Error: outbox.NotConfirmed})
	code, body := r.verb(t, c.ID, "retry", fmt.Sprintf(`{"rev":"%d"}`, c.Rev))
	if code != 409 || !strings.Contains(body, "confirm") {
		t.Fatalf("retry without confirm: %d %s", code, body)
	}
	if code, body := r.verb(t, c.ID, "retry", fmt.Sprintf(`{"confirm":"true","rev":"%d"}`, c.Rev)); code != 200 {
		t.Fatalf("retry: %d %s", code, body)
	}
	if code, body := r.verb(t, c.ID, "edit", `{"text":"x"}`); code != 409 {
		t.Fatalf("edit of an approved draft: %d %s", code, body)
	}
	if code, body := r.verb(t, 999, "approve", `{}`); code != 404 {
		t.Fatalf("unknown draft: %d %s", code, body)
	}
	if code, body := r.verb(t, a.ID, "next", `{}`); code != 409 {
		t.Fatalf("next on a draft: %d %s", code, body)
	}
}

func TestOutboxVerbsRefuseADisabledPlugin(t *testing.T) {
	r := newOutboxRig(t, "slack")
	d := r.stored(t, draft.Draft{Unit: slackUnit, Text: "hi", State: draft.Proposed})
	code, body := r.verb(t, d.ID, "approve", `{}`)
	if code != 409 || !strings.Contains(body, `"plugin":"slack"`) {
		t.Fatalf("%d %s", code, body)
	}
}

func TestObservedDraftIsNotInTheOutbox(t *testing.T) {
	r := newOutboxRig(t)
	r.propose(t, r7u, "watch only", draft.Observe)
	p := r.page(t, "view=outbox")
	if len(p.Entries) != 0 || p.Counters.Views["outbox"] != 0 {
		t.Fatalf("entries %v counters %+v", entryIDs(p.Entries), p.Counters)
	}
}

func TestCatalogServesDraftActions(t *testing.T) {
	r := newOutboxRig(t)
	var res api.ActionsResponse
	if code := get(t, r.s, api.Actions, &res); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	want := map[string]string{"draft.approve": "approve", "draft.edit": "edit", "draft.discard": "dismiss", "draft.retry": "retry"}
	for id, verb := range want {
		i := slices.IndexFunc(res.Specs, func(s api.ActionSpec) bool { return s.ID == id })
		if i < 0 {
			t.Errorf("%s is not served", id)
			continue
		}
		if s := res.Specs[i]; s.Scope != catalog.ScopeDraft || s.Key != "" || s.Verb != verb || s.Plugin != "" {
			t.Errorf("%s served as %+v", id, s)
		}
	}
	for _, s := range []keys.Surface{keys.Web, keys.TUI} {
		var dispatch map[string]string
		for _, b := range keys.Core {
			if b.Context == keys.List && slices.Contains(b.Surfaces, s) {
				if dispatch == nil {
					dispatch = map[string]string{}
				}
				dispatch[b.Key] = b.Action
			}
		}
		if dispatch["a"] != "approve" || dispatch["Backspace"] != "dismiss" {
			t.Errorf("%s: the core keys dispatch %v", s, dispatch)
		}
	}
}

func TestDraftActionsDisabledWithPlugin(t *testing.T) {
	r := newOutboxRig(t, "slack")
	d := r.stored(t, draft.Draft{Unit: slackUnit, Text: "hi", State: draft.Proposed})
	var got api.Draft
	if code := get(t, r.s, api.DraftPath(int64(d.ID)), &got); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	if len(got.Actions) != 4 {
		t.Fatalf("actions %+v", got.Actions)
	}
	for _, st := range got.Actions {
		if st.ID == "draft.discard" && !st.Enabled || st.ID != "draft.discard" && (st.Enabled || st.Reason != "slack is disabled") {
			t.Errorf("state %+v", st)
		}
	}
	if p := r.page(t, "view=outbox"); len(p.Entries) != 0 || p.Counters.Views["outbox"] != 0 {
		t.Fatalf("a disabled plugin's draft stays in the Outbox: %v", entryIDs(p.Entries))
	}
}

func TestApproveRefusesAStaleRev(t *testing.T) {
	for _, path := range []string{"route", "entry verb"} {
		t.Run(path, func(t *testing.T) {
			r := newOutboxRig(t)
			d := r.propose(t, r7u, "as seen", draft.Always)
			var seen api.Draft
			get(t, r.s, api.DraftPath(int64(d.ID)), &seen)
			if code, _, body := r.act(t, d.ID, "edit", `{"text":"swapped by someone"}`); code != 200 {
				t.Fatalf("edit: %d %s", code, body)
			}
			var code int
			var body string
			if path == "route" {
				code, _, body = r.act(t, d.ID, "approve", fmt.Sprintf(`{"rev":%d}`, seen.Rev))
			} else {
				code, body = r.verb(t, d.ID, "approve", fmt.Sprintf(`{"rev":"%d"}`, seen.Rev))
			}
			if code != 409 || !strings.Contains(body, "changed since you saw it") {
				t.Fatalf("%d %s", code, body)
			}
			if got, _ := r.o.Store.Get(context.Background(), d.ID); got.State != draft.Proposed {
				t.Fatalf("state %s", got.State)
			}
		})
	}
}

func TestApproveWithRevPostsTheTextAtThatRev(t *testing.T) {
	r := newOutboxRig(t)
	d := r.propose(t, r7u, "as seen", draft.Always)
	var seen api.Draft
	get(t, r.s, api.DraftPath(int64(d.ID)), &seen)
	code, got, body := r.act(t, d.ID, "approve", fmt.Sprintf(`{"rev":%d}`, seen.Rev))
	if code != 200 || got.State != "approved" || got.Text != "as seen" || got.Edited {
		t.Fatalf("%d %s", code, body)
	}
}

func TestApproveNeedsTheRev(t *testing.T) {
	r := newOutboxRig(t)
	d := r.propose(t, r7u, "x", draft.Always)
	if code, _, body := r.act(t, d.ID, "approve", `{}`); code != 400 || !strings.Contains(body, "takes the rev") {
		t.Fatalf("route: %d %s", code, body)
	}
	if code, body := r.verb(t, d.ID, "approve", `{}`); code != 400 {
		t.Fatalf("entry verb: %d %s", code, body)
	}
	if code, _, body := r.act(t, d.ID, "approve", `{"text":"typed"}`); code != 400 {
		t.Fatalf("text without rev: %d %s", code, body)
	}
}

func TestRetryRefusesAStaleRev(t *testing.T) {
	r := newOutboxRig(t)
	d := r.stored(t, draft.Draft{Unit: r7u, Text: "failed once", State: draft.Failed, Error: "refused"})
	if code, _, body := r.act(t, d.ID, "retry", `{}`); code != 400 || !strings.Contains(body, "rev") {
		t.Fatalf("no rev: %d %s", code, body)
	}
	if code, _, body := r.act(t, d.ID, "edit", `{"text":"swapped"}`); code != 200 {
		t.Fatalf("edit: %d %s", code, body)
	}
	if code, _, body := r.act(t, d.ID, "retry", fmt.Sprintf(`{"rev":%d}`, d.Rev)); code != 409 || !strings.Contains(body, "changed since you saw it") {
		t.Fatalf("route: %d %s", code, body)
	}
	if code, body := r.verb(t, d.ID, "retry", fmt.Sprintf(`{"rev":"%d"}`, d.Rev)); code != 409 || !strings.Contains(body, "changed since you saw it") {
		t.Fatalf("entry verb: %d %s", code, body)
	}
	if code, body := r.verb(t, d.ID, "retry", fmt.Sprintf(`{"rev":"%d"}`, d.Rev+1)); code != 200 {
		t.Fatalf("current rev: %d %s", code, body)
	}
}

func TestDraftsShowWhereTheyPost(t *testing.T) {
	r := newOutboxRig(t)
	r.fx.whereErr = errors.New("not logged in")
	failed := r.propose(t, r7u, "hi", draft.Always)
	var one api.Draft
	for range 2 {
		if code := get(t, r.s, api.DraftPath(int64(failed.ID)), &one); code != 200 || one.Where != "" {
			t.Fatalf("a failing Where: %d %+v", code, one)
		}
	}
	if n := r.fx.whereCalls(); n != 1 {
		t.Fatalf("a failed lookup ran %d times, want it kept for a while", n)
	}
	r.fx.whereErr = nil
	d := r.propose(t, r7u, "there", draft.Always)
	want := `#general in the thread "acme/api#7"`
	var res api.DraftsResponse
	if code := get(t, r.s, api.Drafts, &res); code != 200 || len(res.Drafts) != 2 || res.Drafts[0].Where != want || res.Drafts[1].Where != "" {
		t.Fatalf("list: %d %+v", code, res)
	}
	if code := get(t, r.s, api.DraftPath(int64(d.ID)), &one); code != 200 || one.Where != want {
		t.Fatalf("one: %d %+v", code, one)
	}
	if n := r.fx.whereCalls(); n != 2 {
		t.Fatalf("Where ran %d times, want once per draft", n)
	}
	sent := r.stored(t, draft.Draft{Unit: r7u, Text: "done", State: draft.Sent})
	if code := get(t, r.s, api.DraftPath(int64(sent.ID)), &one); code != 200 || one.Where != "" || r.fx.whereCalls() != 2 {
		t.Fatalf("a sent draft resolved its destination: %+v", one)
	}
}

func TestAHeldPublisherCostsOnePageBudget(t *testing.T) {
	r := newOutboxRig(t)
	r.s.whereLookup, r.s.wherePage = time.Second, 200*time.Millisecond
	r.fx.whereHangs = true
	for i := range 5 {
		r.propose(t, r7u, fmt.Sprint("draft ", i), draft.Always)
	}
	for _, read := range []func(){
		func() { r.page(t, "view=outbox") },
		func() { get(t, r.s, api.Drafts, nil) },
	} {
		start := time.Now()
		read()
		if took := time.Since(start); took > 600*time.Millisecond {
			t.Fatalf("a page took %v under a held publisher, want one page budget", took)
		}
	}
	before := r.fx.whereCalls()
	start := time.Now()
	p := r.page(t, "view=outbox")
	if took := time.Since(start); took > 100*time.Millisecond || r.fx.whereCalls() != before {
		t.Fatalf("a refresh took %v and looked up %d again, want the failures kept", took, r.fx.whereCalls()-before)
	}
	for _, e := range p.Entries {
		if e.Draft == nil || e.Draft.Where != "" {
			t.Fatalf("entry %+v", e)
		}
	}
}

func TestDraftTitleNamesWhereItGoes(t *testing.T) {
	u := unit.Unit{Provider: "slack", Key: "T0001/C0001/1700000000.000100"}
	if got := draftTitle(draft.Draft{Unit: u, Kind: draft.Reply, Text: "on it\nmore"}, "#general"); got != "reply on #general: on it" {
		t.Errorf("title %q", got)
	}
	if got := draftTitle(draft.Draft{Unit: u, Kind: draft.React, Reaction: "eyes"}, "#general"); got != "react :eyes: on #general" {
		t.Errorf("title %q", got)
	}
}

type unitItems map[unit.Unit][]items.Item

func (u unitItems) Items(_ context.Context, x unit.Unit) ([]items.Item, error) { return u[x], nil }

func TestADraftNamesItsUnitTheMentionsAndTheItems(t *testing.T) {
	r := newOutboxRig(t)
	r.s.deps.Items = unitItems{r7u: {{ID: "i1", Summary: "Ship the export", State: items.Open, Kind: items.Request,
		WaitingOn: []items.Person{{Key: "slack:U0ANA", Name: "Ana Lima", Aliases: []string{"github:ana"}}}}}}
	d, err := r.o.Propose(context.Background(), outbox.Proposal{Unit: r7u, Items: []string{"i1"}, Kind: draft.Nudge, Text: "@{github:ana}, any news? cc @{github:bob}",
		Origin: draft.Origin{Kind: draft.OriginUser}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.s.readModel().Read(context.Background(), r7u); err != nil {
		t.Fatal(err)
	}
	var one api.Draft
	if code := get(t, r.s, api.DraftPath(int64(d.ID)), &one); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	if one.Label != "acme/api#7" || one.Title == "" || one.Link == "" {
		t.Errorf("presentation %q %q %q", one.Label, one.Title, one.Link)
	}
	if !maps.Equal(one.Names, map[string]string{"github:ana": "Ana Lima"}) {
		t.Errorf("names %v", one.Names)
	}
	if len(one.ItemDetails) != 1 || one.ItemDetails[0].Summary != "Ship the export" || one.ItemDetails[0].State != "open" {
		t.Errorf("items %+v", one.ItemDetails)
	}
	var list api.DraftsResponse
	if code := get(t, r.s, api.Drafts, &list); code != 200 || len(list.Drafts) != 1 || list.Drafts[0].Names["github:ana"] != "Ana Lima" {
		t.Fatalf("list: %d %+v", code, list)
	}
}
