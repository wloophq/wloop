package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/gates"
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
	*tasksRig
	fx    *fakeFx
	o     *outbox.Service
	clock *moveClock
}

// newOutboxRig serves the Outbox over the tasks rig: github publishes through fx, slack is a plugin with no
// publisher, and off names the plugins that are disabled.
func newOutboxRig(t *testing.T, off ...unit.ProviderID) *outboxRig {
	t.Helper()
	r := &outboxRig{fx: &fakeFx{}, clock: &moveClock{now: tasksNow}}
	r.tasksRig = newTasksRig(t, func(d *Deps) {
		if len(off) > 0 {
			d.PluginEnabled = func(p unit.ProviderID) bool { return !slices.Contains(off, p) }
		}
	})
	st := r.db.Gates(r.clock.Now)
	svc := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{r.s.loaded().Gates}, Clock: r.clock,
		Publisher: publishTo(func(ctx context.Context, g gate.Gate) error { return r.o.Publish(ctx, g) })})
	r.s.deps.Gates, r.s.deps.GateReader = svc, st
	r.o = &outbox.Service{Store: r.db.Drafts(), Gates: rigGates{svc, st}, Clock: r.clock, Effects: func(p unit.ProviderID) (port.Effects, error) {
		if p == unit.GitHub && !slices.Contains(off, p) {
			return r.fx, nil
		}
		return nil, fmt.Errorf("%w: %s", outbox.ErrDisabled, p)
	}, Posts: func(d draft.Delivery) bool { return d == draft.CommentDelivery }}
	r.s.deps.Outbox = r.o
	r.s.registerOutbox()
	return r
}

func (r *outboxRig) propose(t *testing.T, u unit.Unit, text string, mode draft.ApprovalMode) draft.Draft {
	t.Helper()
	a := draft.Approval{Mode: mode}
	if mode == draft.Delay {
		a.Window = time.Minute
	}
	d, err := r.o.Propose(context.Background(), outbox.Proposal{Unit: u, Kind: draft.Reply, Text: text,
		Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "test", Key: text}, Policy: draft.Fixed(a)})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

type publishTo func(ctx context.Context, g gate.Gate) error

func (p publishTo) Publish(ctx context.Context, g gate.Gate) error { return p(ctx, g) }

type rigGates struct {
	*gates.Service
	r gates.Reader
}

func (g rigGates) Where(ctx context.Context, q gates.Query) ([]gate.Gate, error) {
	return g.r.Where(ctx, q)
}

// stored puts d in the store as is, for states no service call reaches here, with the gate its state implies: open
// while it is held, approved by a person once it is queued, none once it is dropped.
func (r *outboxRig) stored(t *testing.T, d draft.Draft) draft.Draft {
	t.Helper()
	ctx := context.Background()
	if d.Kind == "" {
		d.Kind = draft.Reply
	}
	d.Original = d.Text
	d.CreatedAt, d.UpdatedAt = r.clock.Now(), r.clock.Now()
	got, err := r.db.Drafts().Insert(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	g := gate.Gate{Kind: gate.KindDraft, Unit: got.Unit, Subject: strconv.FormatInt(int64(got.ID), 10)}
	switch got.State {
	case draft.Held:
		_, err = r.s.deps.Gates.Open(ctx, g, nil)
	case draft.Dropped:
	default:
		g.By, g.Intent = task.ActorPerson, "approve"
		_, err = r.s.deps.Gates.RecordSent(ctx, g)
	}
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// act runs action on draft id as a client does: approve and discard resolve its open gate, the rest, and a
// discard of a draft its gate approved, run its task's verb. body is the action's arguments.
func (r *outboxRig) act(t *testing.T, id draft.ID, action, body string) (int, api.Draft, string) {
	t.Helper()
	var a struct {
		Rev     *int64  `json:"rev"`
		Text    *string `json:"text"`
		Reason  string  `json:"reason"`
		Confirm bool    `json:"confirm"`
	}
	if err := json.Unmarshal([]byte(cmp.Or(body, "{}")), &a); err != nil {
		t.Fatal(err)
	}
	args := map[string]string{}
	if a.Rev != nil {
		args["rev"] = strconv.FormatInt(*a.Rev, 10)
	}
	if a.Text != nil {
		args["text"] = *a.Text
	}
	if a.Reason != "" {
		args["reason"] = a.Reason
	}
	if a.Confirm {
		args["confirm"] = "true"
	}
	req, _ := json.Marshal(api.TaskRequest{Args: args, Source: "cli"})
	var path string
	d, err := r.o.Store.Get(context.Background(), id)
	g, open, _ := r.o.GateOf(context.Background(), d)
	switch {
	case err != nil:
		path = api.TaskPath("draft:"+strconv.FormatInt(int64(id), 10), action)
	case (action == "approve" || action == "discard") && open && g.Status == gate.Open:
		path = api.GatesPath + "/" + strconv.FormatInt(g.ID, 10) + "/" + action
	case action == "discard":
		path = api.TaskPath("draft:"+strconv.FormatInt(int64(id), 10), "dismiss")
	default:
		path = api.TaskPath("draft:"+strconv.FormatInt(int64(id), 10), action)
	}
	rec := post(t, r.s, path, string(req))
	var got api.Draft
	if rec.Code == 200 {
		var res api.DraftsResponse
		if code := get(t, r.s, api.Drafts+"?id="+strconv.FormatInt(int64(id), 10), &res); code != 200 || len(res.Drafts) != 1 {
			t.Fatalf("reading draft %d back: %d", id, code)
		}
		got = res.Drafts[0]
	}
	return rec.Code, got, rec.Body.String()
}

func TestDraftsListByState(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	first := r.propose(t, r7u, "first", draft.Wait)
	r.clock.set(tasksNow.Add(time.Minute))
	second := r.propose(t, r7u, "second\nmore", draft.Wait)
	r.propose(t, r7u, "watched", draft.DryRun)
	var res api.DraftsResponse
	if code := get(t, r.s, api.Drafts+"?state=held", &res); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	var ids []int64
	for _, d := range res.Drafts {
		ids = append(ids, d.ID)
		if d.State != "held" || d.Unit != "github/acme/api#7" || d.Gate == 0 || d.Deadline != nil || d.Intents["approved"] != "approve" ||
			d.Intents["rejected"] != "discard" || len(d.Actions) != 2 {
			t.Errorf("draft %+v", d)
		}
	}
	if !slices.Equal(ids, []int64{int64(second.ID), int64(first.ID)}) {
		t.Fatalf("ids %v, want newest first", ids)
	}
	if code := get(t, r.s, api.Drafts+"?state=nope", nil); code != 400 {
		t.Fatalf("unknown state: %d", code)
	}
	var one api.DraftsResponse
	if code := get(t, r.s, api.Drafts+"?id="+strconv.FormatInt(int64(second.ID), 10), &one); code != 200 || len(one.Drafts) != 1 || one.Drafts[0].Text != "second\nmore" {
		t.Fatalf("GET one: %d %+v", code, one)
	}
}

func TestApproveWithEditedText(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	d := r.propose(t, r7u, "Thanks, merging", draft.Wait)
	code, got, body := r.act(t, d.ID, "approve", `{"rev":0,"text":"Thanks! Merging now."}`)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if got.State != "queued" || got.Text != "Thanks! Merging now." || got.Original != "Thanks, merging" || !got.Edited || got.Intents != nil {
		t.Fatalf("approved %+v", got)
	}
	if calls := r.fx.seen(); len(calls) != 0 {
		t.Fatalf("the route published: %v", calls)
	}
}

func TestApproveAnswers409WhenPluginDisabled(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t, "slack")
	d := r.stored(t, draft.Draft{Unit: slackUnit, Text: "hi", State: draft.Held})
	code, _, body := r.act(t, d.ID, "approve", `{}`)
	var e struct{ Error, Plugin string }
	json.Unmarshal([]byte(body), &e)
	if code != 409 || e.Plugin != "slack" || e.Error != "slack is disabled" {
		t.Fatalf("%d %s", code, body)
	}
	if got, _ := r.o.Store.Get(context.Background(), d.ID); got.State != draft.Held {
		t.Fatalf("state %s", got.State)
	}
}

func TestDraftActionStatuses(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	d := r.propose(t, r7u, "hello", draft.Wait)
	cases := []struct {
		name   string
		id     draft.ID
		action string
		body   string
		code   int
		has    string
	}{
		{"retry of a held draft", d.ID, "retry", `{"rev":0}`, 409, "held"},
		{"unknown id", 999, "approve", `{}`, 404, "draft:999"},
		{"empty text", d.ID, "edit", `{"text":"  "}`, 400, "empty text"},
		{"edit without text", d.ID, "edit", `{}`, 400, "text"},
		{"unknown verb", d.ID, "post", `{}`, 400, "unknown verb"},
	}
	for _, c := range cases {
		code, _, body := r.act(t, c.id, c.action, c.body)
		if code != c.code || !strings.Contains(body, c.has) {
			t.Errorf("%s: %d %s", c.name, code, body)
		}
	}
	if code, got, body := r.act(t, d.ID, "discard", `{}`); code != 200 || got.State != "dropped" || got.Reason != "user" {
		t.Fatalf("discard: %d %s", code, body)
	}
	if code, _, body := r.act(t, d.ID, "approve", `{"rev":1,"text":"x"}`); code != 409 || !strings.Contains(body, "rejected") {
		t.Fatalf("approve of a dropped draft: %d %s", code, body)
	}
	if code := get(t, r.s, api.Drafts+"?id=x", nil); code != 400 {
		t.Fatalf("GET a bad id: %d", code)
	}
}

func TestEnabledPluginWithoutPublisherAnswers409(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	d := r.stored(t, draft.Draft{Unit: slackUnit, Text: "hi", State: draft.Held})
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

func (c draftClock) Now() time.Time { return tasksNow }

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
	t.Parallel()
	r := newOutboxRig(t)
	r.fx.posted = make(chan draft.ID, 4)
	d := r.propose(t, r7u, "ship it", draft.Wait)
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

func TestADelayDraftIsSentOnceItsGateWindowPasses(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.fx.posted = make(chan draft.ID, 4)
	d := r.propose(t, r7u, "auto", draft.Delay)
	clock := r.startSender(t)
	if w := clock.next(t); w != draftWatchdog {
		t.Fatalf("idle wait %v", w)
	}
	if len(r.fx.seen()) != 0 {
		t.Fatal("published before the window passed")
	}
	r.clock.set(tasksNow.Add(time.Minute))
	if err := r.s.deps.Gates.Tick(context.Background(), r.clock.Now()); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-r.fx.posted:
		if id != d.ID {
			t.Fatalf("published %d", id)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the gate's release did not wake the sender")
	}
	g, _, _ := r.o.GateOf(context.Background(), d)
	if g.Status != gate.Approved || g.Rule != gates.RuleWindow {
		t.Fatalf("gate %+v", g)
	}
}

func TestSenderRecoversBeforePublishing(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.fx.found = true
	stuck := r.stored(t, draft.Draft{Unit: r7u, Text: "was sending", State: draft.Sending, SendingAt: tasksNow})
	ready := r.stored(t, draft.Draft{Unit: r7u, Text: "approved", State: draft.Queued})
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
	t.Parallel()
	r := newOutboxRig(t)
	a := r.propose(t, r7u, "one", draft.Wait)
	b := r.propose(t, r7u, "two\nsecond line", draft.Wait)
	sent := r.propose(t, r7u, "three", draft.Wait)
	if code, _, body := r.act(t, sent.ID, "approve", `{"rev":0}`); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if _, _, err := r.o.Send(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := r.page(t, "view=outbox")
	if got, want := taskIDs(p.Tasks), []string{fmt.Sprintf("draft:%d", a.ID), fmt.Sprintf("draft:%d", b.ID)}; !slices.Equal(got, want) {
		t.Fatalf("tasks %v, want %v", got, want)
	}
	e := p.Tasks[1]
	if ed, ok := e.DraftOf(); !ok || ed.ID != int64(b.ID) || e.Title != "reply on acme/api#7: two" || !slices.Equal(e.Units, []unit.Unit{r7u}) ||
		!slices.Equal(e.UnitLabels, []string{"acme/api#7"}) {
		t.Fatalf("task %+v", e)
	}
	if p.Counters.Views["outbox"] != 2 || p.Counters.Badge != p.Counters.Views["inbox"]+p.Counters.Views["needs-me"] {
		t.Fatalf("counters %+v", p.Counters)
	}
	var c api.Counters
	if get(t, r.s, api.CountersPath, &c); c.Views["outbox"] != 2 {
		t.Fatalf("counters route %+v", c)
	}
	var detail api.TaskView
	code := get(t, r.s, api.TaskPath(e.ID), &detail)
	if dd, ok := detail.DraftOf(); code != 200 || !ok || dd.Text != "two\nsecond line" {
		t.Fatalf("detail %d %+v", code, detail)
	}
	verbs := map[string]bool{}
	for _, v := range detail.Verbs {
		verbs[v.Verb] = v.Enabled
	}
	if !verbs["approve"] || !verbs["edit"] || !verbs["dismiss"] || verbs["retry"] || verbs["next"] || verbs["done"] {
		t.Fatalf("verbs %+v", detail.Verbs)
	}
	if p := r.page(t, "view=outbox&context=@reply"); len(p.Tasks) != 0 {
		t.Fatalf("a context filter keeps %v", taskIDs(p.Tasks))
	}
}

func TestSendingDraftStaysVisibleBeforeRecovery(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	d := r.stored(t, draft.Draft{Unit: r7u, Text: "in flight", State: draft.Sending, SendingAt: tasksNow})
	p := r.page(t, "view=outbox")
	if !slices.Equal(taskIDs(p.Tasks), []string{fmt.Sprintf("draft:%d", d.ID)}) || p.Counters.Views["outbox"] != 1 {
		t.Fatalf("tasks %v counters %+v", taskIDs(p.Tasks), p.Counters)
	}
	for _, v := range p.Tasks[0].Verbs {
		if v.Enabled {
			t.Errorf("%s enabled on a sending draft", v.Verb)
		}
	}
}

// getDraft reads draft id into d, as GET drafts?id= serves it, and answers the status.
func (r *outboxRig) getDraft(t *testing.T, id draft.ID, d *api.Draft) int {
	t.Helper()
	var res api.DraftsResponse
	code := get(t, r.s, api.Drafts+"?id="+strconv.FormatInt(int64(id), 10), &res)
	if code == 200 && len(res.Drafts) == 1 {
		*d = res.Drafts[0]
	}
	return code
}

func (r *outboxRig) verb(t *testing.T, id draft.ID, verb, args string) (int, string) {
	t.Helper()
	rec := post(t, r.s, api.TaskPath(fmt.Sprintf("draft:%d", id), verb), `{"source":"cockpit","args":`+args+`}`)
	return rec.Code, rec.Body.String()
}

func TestOutboxVerbsActOnTheDraft(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	ctx := context.Background()
	a := r.propose(t, r7u, "approve me", draft.Wait)
	if code, body := r.verb(t, a.ID, "approve", `{"rev":"0","text":"approved text"}`); code != 200 || !strings.Contains(body, `"state":"next"`) {
		t.Fatalf("approve: %d %s", code, body)
	}
	if got, _ := r.o.Store.Get(ctx, a.ID); got.State != draft.Queued || got.Text != "approved text" {
		t.Fatalf("approved %+v", got)
	}
	if code, body := r.verb(t, a.ID, "approve", `{"rev":"1","text":"again"}`); code != 409 {
		t.Fatalf("approve twice: %d %s", code, body)
	}
	b := r.propose(t, r7u, "drop me", draft.Wait)
	if code, body := r.verb(t, b.ID, "dismiss", `{}`); code != 200 {
		t.Fatalf("dismiss: %d %s", code, body)
	}
	if got, _ := r.o.Store.Get(ctx, b.ID); got.State != draft.Dropped || got.Reason != "user" {
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
	t.Parallel()
	r := newOutboxRig(t, "slack")
	d := r.stored(t, draft.Draft{Unit: slackUnit, Text: "hi", State: draft.Held})
	code, body := r.verb(t, d.ID, "approve", `{}`)
	if code != 409 || !strings.Contains(body, `"plugin":"slack"`) {
		t.Fatalf("%d %s", code, body)
	}
}

func TestObservedDraftIsNotInTheOutbox(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.propose(t, r7u, "watch only", draft.DryRun)
	p := r.page(t, "view=outbox")
	if len(p.Tasks) != 0 || p.Counters.Views["outbox"] != 0 {
		t.Fatalf("tasks %v counters %+v", taskIDs(p.Tasks), p.Counters)
	}
}

func TestCatalogServesDraftActions(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	var res api.ActionsResponse
	if code := get(t, r.s, api.Actions, &res); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	want := map[string]string{"draft.edit": "edit", "draft.retry": "retry"}
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
	t.Parallel()
	r := newOutboxRig(t, "slack")
	d := r.stored(t, draft.Draft{Unit: slackUnit, Text: "hi", State: draft.Held})
	var got api.Draft
	if code := r.getDraft(t, d.ID, &got); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	if len(got.Actions) != 2 {
		t.Fatalf("actions %+v", got.Actions)
	}
	for _, st := range got.Actions {
		if st.Enabled || st.Reason != "slack is disabled" {
			t.Errorf("state %+v", st)
		}
	}
	if p := r.page(t, "view=outbox"); len(p.Tasks) != 0 || p.Counters.Views["outbox"] != 0 {
		t.Fatalf("a disabled plugin's draft stays in the Outbox: %v", taskIDs(p.Tasks))
	}
}

func TestApproveRefusesAStaleRev(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"route", "task verb"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			r := newOutboxRig(t)
			d := r.propose(t, r7u, "as seen", draft.Wait)
			var seen api.Draft
			r.getDraft(t, d.ID, &seen)
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
			if got, _ := r.o.Store.Get(context.Background(), d.ID); got.State != draft.Held {
				t.Fatalf("state %s", got.State)
			}
			old := api.GatesPath + "/" + strconv.FormatInt(seen.Gate, 10) + "/approve"
			if rec := post(t, r.s, old, `{"source":"cli"}`); rec.Code != 409 {
				t.Fatalf("approving the gate the edit superseded: %d %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestApproveWithRevPostsTheTextAtThatRev(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	d := r.propose(t, r7u, "as seen", draft.Wait)
	var seen api.Draft
	r.getDraft(t, d.ID, &seen)
	code, got, body := r.act(t, d.ID, "approve", fmt.Sprintf(`{"rev":%d}`, seen.Rev))
	if code != 200 || got.State != "queued" || got.Text != "as seen" || got.Edited {
		t.Fatalf("%d %s", code, body)
	}
}

func TestTheDraftVerbNeedsTheRevAndTheGatePathItsGate(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	d := r.propose(t, r7u, "x", draft.Wait)
	if code, body := r.verb(t, d.ID, "approve", `{}`); code != 400 || !strings.Contains(body, "takes the rev") {
		t.Fatalf("task verb: %d %s", code, body)
	}
	if code, body := r.verb(t, d.ID, "approve", `{"text":"typed"}`); code != 400 {
		t.Fatalf("text without rev: %d %s", code, body)
	}
	if code, got, body := r.act(t, d.ID, "approve", `{}`); code != 200 || got.State != "queued" || got.Text != "x" {
		t.Fatalf("the gate path approves the gate it names: %d %s", code, body)
	}
}

func TestRetryRefusesAStaleRev(t *testing.T) {
	t.Parallel()
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
		t.Fatalf("task verb: %d %s", code, body)
	}
	if code, body := r.verb(t, d.ID, "retry", fmt.Sprintf(`{"rev":"%d"}`, d.Rev+1)); code != 200 {
		t.Fatalf("current rev: %d %s", code, body)
	}
}

func TestDraftsShowWhereTheyPost(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.fx.whereErr = errors.New("not logged in")
	failed := r.propose(t, r7u, "hi", draft.Wait)
	var one api.Draft
	for range 2 {
		if code := r.getDraft(t, failed.ID, &one); code != 200 || one.Where != "" {
			t.Fatalf("a failing Where: %d %+v", code, one)
		}
	}
	if n := r.fx.whereCalls(); n != 1 {
		t.Fatalf("a failed lookup ran %d times, want it kept for a while", n)
	}
	r.fx.whereErr = nil
	d := r.propose(t, r7u, "there", draft.Wait)
	want := `#general in the thread "acme/api#7"`
	var res api.DraftsResponse
	if code := get(t, r.s, api.Drafts, &res); code != 200 || len(res.Drafts) != 2 || res.Drafts[0].Where != want || res.Drafts[1].Where != "" {
		t.Fatalf("list: %d %+v", code, res)
	}
	if code := r.getDraft(t, d.ID, &one); code != 200 || one.Where != want {
		t.Fatalf("one: %d %+v", code, one)
	}
	if n := r.fx.whereCalls(); n != 2 {
		t.Fatalf("Where ran %d times, want once per draft", n)
	}
	sent := r.stored(t, draft.Draft{Unit: r7u, Text: "done", State: draft.Sent})
	if code := r.getDraft(t, sent.ID, &one); code != 200 || one.Where != "" || r.fx.whereCalls() != 2 {
		t.Fatalf("a sent draft resolved its destination: %+v", one)
	}
}

func TestAHeldPublisherCostsOnePageBudget(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newOutboxRig(t)
		const budget = 200 * time.Millisecond
		r.s.whereLookup, r.s.wherePage = time.Second, budget
		r.fx.whereHangs = true
		// The pull request's task holds the gates of the drafts proposed on it, as the daemon's gates service opens them.
		holder := task.ID("t-0000000000000007")
		r.put(t, task.Task{ID: holder, State: "next", Category: task.CategoryReady, Title: "the pull request", Units: []unit.Unit{r7u}})
		for i := range 5 {
			d := r.propose(t, r7u, fmt.Sprint("draft ", i), draft.Wait)
			if _, err := r.s.deps.Gates.Open(context.Background(), gate.Gate{Task: holder, Unit: r7u, Kind: gate.KindDraft, Subject: fmt.Sprint(d.ID)}, nil); err != nil {
				t.Fatal(err)
			}
		}
		for name, read := range map[string]func(){
			"the outbox":            func() { r.page(t, "view=outbox") },
			"the task of the gates": func() { get(t, r.s, api.TaskPath("t-0000000000000007"), nil) },
			"the drafts":            func() { get(t, r.s, api.Drafts, nil) },
			"the gates":             func() { get(t, r.s, api.GatesPath, nil) },
		} {
			r.s.draftWheres.Clear()
			start := time.Now()
			read()
			if took := time.Since(start); took != budget {
				t.Fatalf("%s took %v under a held publisher, want one page budget, %v", name, took, budget)
			}
		}
		before := r.fx.whereCalls()
		start := time.Now()
		p := r.page(t, "view=outbox")
		if took := time.Since(start); took != 0 || r.fx.whereCalls() != before {
			t.Fatalf("a refresh took %v and looked up %d again, want the failures kept", took, r.fx.whereCalls()-before)
		}
		for _, e := range p.Tasks {
			if d, ok := e.DraftOf(); !ok || d.Where != "" {
				t.Fatalf("task %+v", e)
			}
		}
	})
}

func TestDraftTitleNamesWhereItGoes(t *testing.T) {
	t.Parallel()
	u := unit.Unit{Provider: "slack", Key: "T0001/C0001/1700000000.000100"}
	if got := draftTitle(draft.Draft{Unit: u, Purpose: "reply", Text: "on it\nmore"}, "#general"); got != "reply on #general: on it" {
		t.Errorf("title %q", got)
	}
	if got := draftTitle(draft.Draft{Unit: u, Purpose: "react", Delivery: draft.Reaction, Reaction: "eyes"}, "#general"); got != "react :eyes: on #general" {
		t.Errorf("title %q", got)
	}
}

type unitItems map[unit.Unit][]items.Item

func (u unitItems) Items(_ context.Context, x unit.Unit) ([]items.Item, error) { return u[x], nil }

func TestADraftNamesItsUnitTheMentionsAndTheItems(t *testing.T) {
	t.Parallel()
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
	if code := r.getDraft(t, d.ID, &one); code != 200 {
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

type takingFx struct{ *fakeFx }

func (takingFx) Takes(target string) bool { return strings.HasPrefix(target, "#") }

func TestADraftOnATaskActsThroughThePublisherOfItsTarget(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.o.Effects = func(p unit.ProviderID) (port.Effects, error) {
		if p == unit.GitHub {
			return takingFx{r.fx}, nil
		}
		return nil, fmt.Errorf("%w: %s", outbox.ErrDisabled, p)
	}
	r.o.Targets = []unit.ProviderID{unit.GitHub}
	r.put(t, task.Task{ID: "e1", State: "next", Category: task.CategoryReady, Title: "a task"})
	d := r.stored(t, draft.Draft{Unit: unit.Unit{Provider: unit.Task, Key: "e1"}, Target: "#general", Text: "hi", State: draft.Held})
	var got api.Draft
	if code := r.getDraft(t, d.ID, &got); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	for _, st := range got.Actions {
		if st.ID == "draft.edit" && !st.Enabled {
			t.Errorf("state %+v", st)
		}
	}
	if got.Intents["approved"] == "" {
		t.Errorf("its gate offers no approval: %+v", got)
	}
	if code, _, body := r.act(t, d.ID, "approve", fmt.Sprintf(`{"rev":%d}`, got.Rev)); code != 200 {
		t.Fatalf("approve: %d %s", code, body)
	}
}

func TestADraftGateServesItsDraftAndItsDelivery(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	held := r.propose(t, r7u, "waits", draft.Wait)
	failed := r.propose(t, r7u, "fails", draft.Wait)
	if code, _, body := r.act(t, failed.ID, "approve", `{}`); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	r.fx.fail = errors.New("refused")
	if _, _, err := r.o.Send(context.Background()); err != nil {
		t.Fatal(err)
	}
	var gs []api.GateView
	if code := get(t, r.s, api.GatesPath+"?status=open,approved", &gs); code != 200 || len(gs) != 2 {
		t.Fatalf("%d %+v", code, gs)
	}
	for _, g := range gs {
		var d api.Draft
		if err := json.Unmarshal(g.Subject, &d); err != nil {
			t.Fatalf("gate %d's subject %s: %v", g.ID, g.Subject, err)
		}
		want := map[int64]string{int64(held.ID): "held", int64(failed.ID): "failed"}[d.ID]
		if g.Kind != "draft" || g.Delivery != want || d.State != want || d.Gate != g.ID {
			t.Errorf("gate %+v, draft %+v", g, d)
		}
	}
}

func TestAnAgentCannotChangeADraftThroughItsGate(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	ctx := context.Background()
	d := r.propose(t, r7u, "the person's text", draft.Wait)
	g, _, _ := r.o.GateOf(ctx, d)
	path := api.GatesPath + "/" + strconv.FormatInt(g.ID, 10) + "/approve"
	for _, body := range []string{`{"source":"claude","args":{"text":"agent text"}}`, `{"source":"claude","args":{"text":"agent text","rev":"0"}}`, `{"source":"claude"}`} {
		if rec := postFrom(t, r.s, "term_1", path, body); rec.Code != 403 {
			t.Fatalf("an agent session's approval %s: %d %s", body, rec.Code, rec.Body)
		}
	}
	got, _ := r.o.Store.Get(ctx, d.ID)
	after, _, _ := r.o.GateOf(ctx, got)
	if got.Text != "the person's text" || got.Rev != d.Rev || got.State != draft.Held || after.ID != g.ID || after.Status != gate.Open {
		t.Fatalf("the refused agent changed the draft %+v or its gate %+v", got, after)
	}
	if rec := post(t, r.s, path, `{"source":"cockpit"}`); rec.Code != 200 {
		t.Fatalf("the person's approval: %d %s", rec.Code, rec.Body)
	}
	if _, _, err := r.o.Send(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.o.Store.Get(ctx, d.ID); got.State != draft.Sent || got.Text != "the person's text" || got.Edited {
		t.Fatalf("the person approved %+v", got)
	}
}

func TestApprovingWithOtherTextApprovesTheGateItsEditOpened(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	ctx := context.Background()
	d := r.propose(t, r7u, "first", draft.Wait)
	g, _, _ := r.o.GateOf(ctx, d)
	path := api.GatesPath + "/" + strconv.FormatInt(g.ID, 10) + "/approve"
	if rec := post(t, r.s, path, `{"source":"cockpit","args":{"text":"second","rev":"0"}}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got, _ := r.o.Store.Get(ctx, d.ID)
	ng, _, _ := r.o.GateOf(ctx, got)
	old, _ := r.s.deps.GateReader.Get(ctx, g.ID)
	if got.State != draft.Queued || got.Text != "second" || ng.ID == g.ID || ng.Status != gate.Approved || ng.By != task.ActorPerson || old.Status != gate.Superseded {
		t.Fatalf("draft %+v, its gate %+v, the gate the person saw %+v", got, ng, old)
	}
}
