package daemon

import (
	"context"
	"errors"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/gate"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// pin is a draft an open message pins: the message, and its question gate, 0 while the gate is not opened yet.
type pin struct{ message, question int64 }

// pinsOf report the drafts the open messages pin, by draft id, as the gates service's Options port does: a pin
// holds until its message closes, after its question was answered and the message settled.
type pinsOf struct {
	r    gates.Reader
	mu   *sync.Mutex
	pins map[int64]pin
}

func (p pinsOf) OptionDraft(context.Context, gate.Gate, gates.Resolution) (gate.Gate, int64, bool, error) {
	return gate.Gate{}, 0, false, nil
}

func (p pinsOf) Pinning(ctx context.Context, drafted gate.Gate) (gate.Gate, string, int64, bool, error) {
	id, err := strconv.ParseInt(drafted.Subject, 10, 64)
	if err != nil {
		return gate.Gate{}, "", 0, false, nil
	}
	p.mu.Lock()
	pn, ok := p.pins[id]
	p.mu.Unlock()
	if !ok || pn.question == 0 {
		return gate.Gate{}, "", 0, ok, nil
	}
	q, err := p.r.Get(ctx, pn.question)
	return q, "", 0, err == nil, err
}

func (p pinsOf) PinnedDrafts(context.Context) (map[int64]int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[int64]int64{}
	for d, pn := range p.pins {
		out[d] = pn.message
	}
	return out, nil
}

// queueRig is a tasks rig whose gates open at a chosen age and whose questions may pin drafts.
type queueRig struct {
	*tasksRig
	mu   sync.Mutex
	pins map[int64]pin
}

func newQueueRig(t *testing.T, edit func(*Deps)) *queueRig {
	t.Helper()
	q := &queueRig{tasksRig: newTasksRig(t, edit), pins: map[int64]pin{}}
	st := q.db.Gates(func() time.Time { return tasksNow })
	q.s.deps.Gates = gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{q.s.loaded().Gates}, Clock: fixedClock(tasksNow),
		Options: pinsOf{st, &q.mu, q.pins}})
	q.s.deps.GateReader = st
	return q
}

// open opens a gate of kind on task id, subject subject, age before now.
func (q *queueRig) open(t *testing.T, id task.ID, kind gate.Kind, subject string, age time.Duration) gate.Gate {
	t.Helper()
	at := tasksNow.Add(-age)
	st := q.db.Gates(func() time.Time { return at })
	svc := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{q.s.loaded().Gates}, Clock: fixedClock(at)})
	g, err := svc.Open(context.Background(), gate.Gate{Unit: task.UnitOf(id), Kind: kind, Subject: subject}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func (q *queueRig) all(t *testing.T) api.Queue {
	t.Helper()
	var out api.Queue
	if code := get(t, q.s, api.QueueAllPath, &out); code != 200 {
		t.Fatalf("GET %s: %d", api.QueueAllPath, code)
	}
	return out
}

func kindsOf(q api.Queue) []string {
	out := []string{}
	for _, it := range q.Items {
		out = append(out, it.Kind)
	}
	return out
}

func TestQueueAllMergesKindsInProfileOrder(t *testing.T) {
	t.Parallel()
	q := newQueueRig(t, nil)
	q.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "send the notes"},
		task.Task{ID: "t-0000000000000002", State: "next", Category: task.CategoryReady, Title: "pick a name"},
		task.Task{ID: "t-0000000000000003", State: "next", Category: task.CategoryReady, Title: "plan the move"})
	q.open(t, "t-0000000000000001", gate.KindDraft, "41", 2*time.Hour)
	q.open(t, "t-0000000000000002", gate.KindQuestion, "7", time.Hour)
	q.open(t, "t-0000000000000003", gate.KindPlan, "1", 3*time.Hour)
	got := q.all(t)
	if want := []string{"answer", "send-draft", "approve-plan"}; !slices.Equal(kindsOf(got), want) {
		t.Fatalf("kinds %v, want %v", kindsOf(got), want)
	}
	if got.Kind != "" || got.Title != "Needs you" || got.Empty != "Nothing needs you." || got.Total != 3 {
		t.Fatalf("queue %+v, want the profile's queue_all and empty_all over 3 items", got)
	}
	for _, it := range got.Items {
		if it.Gate == nil || it.Primary.Kind != "gate" || it.Primary.Label == "" {
			t.Errorf("item %+v, want its gate and primary", it)
		}
	}
}

func TestQueueAllKindNotInOrderSortsLast(t *testing.T) {
	t.Parallel()
	l := *bundledLoaded()
	l.Decisions.Order = []decision.Kind{decision.SendDraft, decision.Answer}
	q := newQueueRig(t, func(d *Deps) { d.Intents = func() *intentload.Loaded { return &l } })
	q.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "send the notes"},
		task.Task{ID: "t-0000000000000002", State: "next", Category: task.CategoryReady, Title: "pick a name"},
		task.Task{ID: "t-0000000000000003", State: "next", Category: task.CategoryReady, Title: "plan the move"})
	q.open(t, "t-0000000000000003", gate.KindPlan, "1", 9*time.Hour)
	q.open(t, "t-0000000000000002", gate.KindQuestion, "7", time.Hour)
	q.open(t, "t-0000000000000001", gate.KindDraft, "41", 2*time.Hour)
	if got, want := kindsOf(q.all(t)), []string{"send-draft", "answer", "approve-plan"}; !slices.Equal(got, want) {
		t.Fatalf("kinds %v, want %v", got, want)
	}
}

func TestQueueAllTaskWithTwoGatesYieldsTwoItems(t *testing.T) {
	t.Parallel()
	q := newQueueRig(t, nil)
	q.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "ship the retries"})
	question := q.open(t, "t-0000000000000001", gate.KindQuestion, "7", time.Hour)
	drafted := q.open(t, "t-0000000000000001", gate.KindDraft, "41", 2*time.Hour)
	got := q.all(t)
	if len(got.Items) != 2 || got.Items[0].Gate.ID != question.ID || got.Items[1].Gate.ID != drafted.ID {
		t.Fatalf("items %+v, want the question's then the draft's", got.Items)
	}
	for _, it := range got.Items {
		if it.Task.ID != "t-0000000000000001" {
			t.Errorf("item of task %s", it.Task.ID)
		}
	}
}

func TestQueueAllAcceptsTheLens(t *testing.T) {
	t.Parallel()
	q := newQueueRig(t, nil)
	q.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "ship the retries"},
		task.Task{ID: "t-0000000000000002", State: "next", Category: task.CategoryReady, Title: "call Ada"})
	q.open(t, "t-0000000000000001", gate.KindQuestion, "7", time.Hour)
	q.open(t, "t-0000000000000002", gate.KindDraft, "41", time.Hour)
	var narrowed api.Queue
	if code := get(t, q.s, api.QueueAllPath+"?q=retries", &narrowed); code != 200 {
		t.Fatalf("%d", code)
	}
	if narrowed.Total != 1 || narrowed.Items[0].Task.ID != "t-0000000000000001" {
		t.Fatalf("narrowed %+v", narrowed)
	}
}

func TestQueueItemCarriesKindOnPerKindRoute(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "ship the retries"})
	r.withGates(t, "t-0000000000000001")
	var q api.Queue
	if code := get(t, r.s, queuePath("answer"), &q); code != 200 || len(q.Items) != 1 {
		t.Fatalf("%d %+v", code, q)
	}
	if q.Items[0].Kind != "answer" {
		t.Fatalf("item kind %q, want answer", q.Items[0].Kind)
	}
}

// setPin makes message pin draft id, its question gate question (0 while not opened); closed unpins it.
func (q *queueRig) setPin(id int64, p pin, closed bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if closed {
		delete(q.pins, id)
		return
	}
	q.pins[id] = p
}

// pinnedQuestion opens, on task id, a question whose options a and b pin drafts d1 and d2, b recommended.
func (q *queueRig) pinnedQuestion(t *testing.T, id task.ID) (question, d1, d2 gate.Gate) {
	t.Helper()
	d1 = q.open(t, id, gate.KindDraft, "101", 3*time.Hour)
	d2 = q.open(t, id, gate.KindDraft, "102", 3*time.Hour)
	question = q.open(t, id, gate.KindQuestion, "9", time.Hour)
	q.setPin(101, pin{9, question.ID}, false)
	q.setPin(102, pin{9, question.ID}, false)
	m := dmessage.Message{ID: 9, Kind: dmessage.Question, Text: "Which reply?", Recommend: "b",
		Options: []record.Option{{ID: "a", Label: "Short", Draft: &record.DraftRef{ID: 101, Rev: 1}},
			{ID: "b", Label: "Long", Draft: &record.DraftRef{ID: 102, Rev: 1}}, {ID: "c", Label: "Neither"}}}
	q.s.deps.Messages = &messages.Service{Store: openMessages{m}, Books: noBooks{}}
	return question, d1, d2
}

func TestQueueAllFoldsOptionDraftsIntoQuestion(t *testing.T) {
	t.Parallel()
	q := newQueueRig(t, nil)
	q.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "answer the vendor"})
	question, d1, d2 := q.pinnedQuestion(t, "t-0000000000000001")
	got := q.all(t)
	if len(got.Items) != 1 || got.Items[0].Kind != "answer" || got.Items[0].Gate.ID != question.ID {
		t.Fatalf("items %+v, want the question alone", got.Items)
	}
	opts := got.Items[0].Options
	if len(opts) != 3 {
		t.Fatalf("options %+v, want the question's three", opts)
	}
	byID := map[string]api.QueueOption{}
	for _, o := range opts {
		byID[o.ID] = o
	}
	a, b, c := byID["a"], byID["b"], byID["c"]
	if a.Label != "Short" || a.Recommended || a.Draft == nil || a.Draft.ID != d1.ID {
		t.Errorf("option a %+v, want draft gate %d", a, d1.ID)
	}
	if !b.Recommended || b.Draft == nil || b.Draft.ID != d2.ID {
		t.Errorf("option b %+v, want recommended with draft gate %d", b, d2.ID)
	}
	if c.Draft != nil || c.Recommended {
		t.Errorf("option c %+v, want no draft", c)
	}
	var drafts api.Queue
	get(t, q.s, queuePath("send-draft"), &drafts)
	if drafts.Total != 0 {
		t.Errorf("the send-draft queue lists %+v, want none of the pinned drafts", drafts.Items)
	}
}

func TestQueueAllFoldsAConfirmsDraftIntoItsYes(t *testing.T) {
	t.Parallel()
	q := newQueueRig(t, nil)
	q.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "answer the vendor"})
	drafted := q.open(t, "t-0000000000000001", gate.KindDraft, "101", 3*time.Hour)
	question := q.open(t, "t-0000000000000001", gate.KindQuestion, "9", time.Hour)
	q.setPin(101, pin{9, question.ID}, false)
	m := dmessage.Message{ID: 9, Kind: dmessage.Question, Text: "Send it?", Confirm: true, Recommend: "yes", Draft: &record.DraftRef{ID: 101, Rev: 1}}
	q.s.deps.Messages = &messages.Service{Store: openMessages{m}, Books: noBooks{}}
	got := q.all(t)
	if len(got.Items) != 1 || got.Items[0].Gate.ID != question.ID {
		t.Fatalf("items %+v, want the confirm alone", got.Items)
	}
	opts := got.Items[0].Options
	if len(opts) != 2 || opts[0].ID != "yes" || opts[1].ID != "no" {
		t.Fatalf("options %+v, want yes and no", opts)
	}
	yes, no := opts[0], opts[1]
	if yes.Draft == nil || yes.Draft.ID != drafted.ID || !yes.Recommended || no.Draft != nil || no.Recommended {
		t.Errorf("yes %+v, no %+v: want the draft on a recommended yes", yes, no)
	}
	if want := q.s.notice(builtin.NoticeNotifyYes, nil); yes.Label != want || no.Label != q.s.notice(builtin.NoticeNotifyNo, nil) {
		t.Errorf("labels %q, %q, want the profile's confirm words", yes.Label, no.Label)
	}
}

// failingMessages fail every read of a message.
type failingMessages struct{ openMessages }

func (failingMessages) Message(context.Context, int64) (dmessage.Message, bool, error) {
	return dmessage.Message{}, false, errors.New("the store is locked")
}

func TestQueueAllFailsWhenAPinningQuestionCannotBeRead(t *testing.T) {
	t.Parallel()
	q := newQueueRig(t, nil)
	q.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "answer the vendor"})
	q.pinnedQuestion(t, "t-0000000000000001")
	q.s.deps.Messages = &messages.Service{Store: failingMessages{}, Books: noBooks{}}
	rec := httptest.NewRecorder()
	q.s.Handler().ServeHTTP(rec, apiRequest("GET", api.QueueAllPath, nil))
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "locked") {
		t.Fatalf("%d %s, want the read's failure, not a question without its drafts", rec.Code, rec.Body)
	}
}

func TestOptionDraftStaysPinnedWhileItsQuestionSettles(t *testing.T) {
	t.Parallel()
	q := newQueueRig(t, nil)
	q.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "answer the vendor"})
	question, _, d2 := q.pinnedQuestion(t, "t-0000000000000001")
	st := q.db.Gates(func() time.Time { return tasksNow })
	if _, err := st.Resolve(context.Background(), question.ID, gates.Resolution{Status: gate.Answered, Intent: "answer", Answer: "c", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if got := q.all(t); len(got.Items) != 0 {
		t.Fatalf("items %+v while the answered message settles, want none: approving its drafts is refused", got.Items)
	}
	q.setPin(101, pin{10, 0}, false)
	q.setPin(102, pin{}, true)
	if got := q.all(t); len(got.Items) != 1 || got.Items[0].Kind != "send-draft" || got.Items[0].Gate.ID != d2.ID {
		t.Fatalf("items %+v, want only the draft no message pins, the other pinned by a question whose gate is not open yet", got.Items)
	}
}

func TestOptionDraftCountsAgainOnceQuestionCloses(t *testing.T) {
	t.Parallel()
	q := newQueueRig(t, nil)
	q.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "answer the vendor"})
	question, d1, d2 := q.pinnedQuestion(t, "t-0000000000000001")
	st := q.db.Gates(func() time.Time { return tasksNow })
	if _, err := st.Resolve(context.Background(), question.ID, gates.Resolution{Status: gate.Answered, Intent: "answer", Answer: "c", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	q.setPin(101, pin{}, true)
	q.setPin(102, pin{}, true)
	got := q.all(t)
	var ids []string
	for _, it := range got.Items {
		ids = append(ids, it.Kind+":"+strconv.FormatInt(it.Gate.ID, 10))
	}
	want := []string{"send-draft:" + strconv.FormatInt(d1.ID, 10), "send-draft:" + strconv.FormatInt(d2.ID, 10)}
	if !slices.Equal(ids, want) {
		t.Fatalf("items %v, want the drafts back as %v once their message closed and they can be sent", ids, want)
	}
}
