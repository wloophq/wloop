package daemon

import (
	"context"
	"slices"
	"strconv"
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
)

// pinsOf reports the question gate whose option pins a draft gate, by the draft's id, as the gates service's
// Options port does: the question as the store holds it now.
type pinsOf struct {
	r    gates.Reader
	pins map[string]int64
}

func (p pinsOf) OptionDraft(context.Context, gate.Gate, gates.Resolution) (gate.Gate, int64, bool, error) {
	return gate.Gate{}, 0, false, nil
}

func (p pinsOf) Pinning(ctx context.Context, drafted gate.Gate) (gate.Gate, string, int64, bool, error) {
	id, ok := p.pins[drafted.Subject]
	if !ok {
		return gate.Gate{}, "", 0, false, nil
	}
	q, err := p.r.Get(ctx, id)
	return q, "", 0, err == nil, err
}

// queueRig is a tasks rig whose gates open at a chosen age and whose questions may pin drafts.
type queueRig struct {
	*tasksRig
	pins map[string]int64
}

func newQueueRig(t *testing.T, edit func(*Deps)) *queueRig {
	t.Helper()
	q := &queueRig{tasksRig: newTasksRig(t, edit), pins: map[string]int64{}}
	st := q.db.Gates(func() time.Time { return tasksNow })
	q.s.deps.Gates = gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{q.s.loaded().Gates}, Clock: fixedClock(tasksNow),
		Options: pinsOf{st, q.pins}})
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

// pinnedQuestion opens, on task id, a question whose options a and b pin drafts d1 and d2, b recommended.
func (q *queueRig) pinnedQuestion(t *testing.T, id task.ID) (question, d1, d2 gate.Gate) {
	t.Helper()
	d1 = q.open(t, id, gate.KindDraft, "101", 3*time.Hour)
	d2 = q.open(t, id, gate.KindDraft, "102", 3*time.Hour)
	question = q.open(t, id, gate.KindQuestion, "9", time.Hour)
	q.pins["101"], q.pins["102"] = question.ID, question.ID
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

func TestOptionDraftCountsAgainOnceQuestionCloses(t *testing.T) {
	t.Parallel()
	q := newQueueRig(t, nil)
	q.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "answer the vendor"})
	question, d1, d2 := q.pinnedQuestion(t, "t-0000000000000001")
	st := q.db.Gates(func() time.Time { return tasksNow })
	if _, err := st.Resolve(context.Background(), question.ID, gates.Resolution{Status: gate.Answered, Intent: "answer", Answer: "c", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	got := q.all(t)
	var ids []string
	for _, it := range got.Items {
		ids = append(ids, it.Kind+":"+strconv.FormatInt(it.Gate.ID, 10))
	}
	want := []string{"send-draft:" + strconv.FormatInt(d1.ID, 10), "send-draft:" + strconv.FormatInt(d2.ID, 10)}
	if !slices.Equal(ids, want) {
		t.Fatalf("items %v, want the drafts back as %v", ids, want)
	}
}
