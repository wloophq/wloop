package daemon

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/gate"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/task"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func TestAQuestionGateOnATaskOutsideTheInboxNotifies(t *testing.T) {
	t.Parallel()
	r := newNotifyRig(t)
	ctx := context.Background()
	put := func(id, state string, cat task.Category) task.Task {
		e := task.Task{ID: task.ID(id), State: task.State(state), Category: cat, Title: id, Origin: task.OriginUser, Created: catalogNow, Updated: catalogNow}
		if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
			t.Fatal(err)
		}
		return e
	}
	ask := func(e task.Task, kind dmessage.Kind, seq int64) {
		m, err := r.s.deps.Messages.Store.Upsert(ctx, dmessage.Message{TaskID: string(e.ID), Unit: task.UnitOf(e.ID), Kind: kind, Text: "which window, " + string(e.ID) + "?",
			Origin: dmessage.Origin{Kind: dmessage.OriginTurn, TurnSeq: seq}, CreatedAt: catalogNow})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.s.deps.Gates.Open(ctx, gate.Gate{Kind: kind.Gate(), Task: e.ID, Unit: task.UnitOf(e.ID), Subject: strconv.FormatInt(m.ID, 10)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	ask(put("e-next", "next", task.CategoryReady), dmessage.Question, 1)
	ask(put("e-wait", "waiting", task.CategoryWaiting), dmessage.AgentPost, 2)
	ask(put("e-prop", "next", task.CategoryReady), dmessage.Proposal, 3)
	put("e-quiet", "next", task.CategoryReady)

	got := r.notified(t)
	for _, id := range []string{"e-next", "e-wait", "e-prop"} {
		e, ok := got[id]
		if !ok || e.Attention.Body != "which window, "+id+"?" {
			t.Errorf("%s: %+v, want its open gate notified", id, e.Attention)
		}
	}
	if _, ok := got["e-quiet"]; ok || len(got) != 3 {
		t.Fatalf("%d notifications: %+v", len(got), got)
	}
	if a := got["e-prop"].Attention; len(a.Actions) == 0 || a.Actions[0].ID != api.TaskActionPrefix+"approve" || a.Actions[0].Title != "Approve" {
		t.Fatalf("a proposal offers %+v, want the verb that approves it, worded by the profile", a.Actions)
	}
}

func TestNotificationWordsComeFromTheProfile(t *testing.T) {
	t.Parallel()
	r := newNotifyRig(t)
	l := *bundledLoaded()
	l.Notices = maps.Clone(l.Notices)
	for name, text := range map[string]string{builtin.NoticeNotifyInbox: "Nova", builtin.NoticeNotifyQuestion: "Pergunta", builtin.NoticeNotifyYes: "Sim",
		builtin.NoticeNotifyNo: "Não", builtin.NoticeNotifyRecommended: "{{.Label}} (sugerido)", builtin.NoticeNotifyReply: "Responder", builtin.NoticeNotifyPost: "Aviso",
		builtin.NoticeNotifyProposal: "Proposta"} {
		l.Notices[name] = text
	}
	r.s.deps.Intents = func() *intentload.Loaded { return &l }
	ctx := context.Background()
	for _, e := range []task.Task{
		{ID: "e-in", State: "inbox", Category: task.CategoryInbox, Title: "plain", Origin: task.OriginUser, Created: catalogNow, Updated: catalogNow},
		{ID: "e-ask", State: "next", Category: task.CategoryReady, Title: "ask", Origin: task.OriginUser, Created: catalogNow, Updated: catalogNow},
	} {
		if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
			t.Fatal(err)
		}
	}
	m, err := r.s.deps.Messages.Store.Upsert(ctx, dmessage.Message{TaskID: "e-ask", Unit: task.UnitOf("e-ask"), Kind: dmessage.Question, Text: "ship?",
		Confirm: true, Recommend: "yes", Origin: dmessage.Origin{Kind: dmessage.OriginTurn, TurnSeq: 1}, CreatedAt: catalogNow})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.deps.Gates.Open(ctx, gate.Gate{Kind: gate.KindQuestion, Task: "e-ask", Unit: task.UnitOf("e-ask"), Subject: strconv.FormatInt(m.ID, 10)}, nil); err != nil {
		t.Fatal(err)
	}
	for i, kind := range []dmessage.Kind{dmessage.AgentPost, dmessage.Proposal} {
		id := task.ID("e-kind-" + strconv.Itoa(i))
		if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: task.Task{ID: id, State: "next", Category: task.CategoryReady, Title: string(id),
			Origin: task.OriginUser, Created: catalogNow, Updated: catalogNow}}}); err != nil {
			t.Fatal(err)
		}
		m, err := r.s.deps.Messages.Store.Upsert(ctx, dmessage.Message{TaskID: string(id), Unit: task.UnitOf(id), Kind: kind, Text: "note",
			Origin: dmessage.Origin{Kind: dmessage.OriginTurn, TurnSeq: int64(i + 2)}, CreatedAt: catalogNow})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.s.deps.Gates.Open(ctx, gate.Gate{Kind: kind.Gate(), Task: id, Unit: task.UnitOf(id), Subject: strconv.FormatInt(m.ID, 10)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	got := r.notified(t)
	if a, b := got["e-kind-0"].Attention, got["e-kind-1"].Attention; a == nil || b == nil || a.Title != "Aviso" || b.Title != "Proposta" {
		t.Fatalf("a post notifies %+v and a proposal %+v", a, b)
	}
	if a := got["e-in"].Attention; a == nil || a.Title != "Nova" {
		t.Fatalf("the Inbox task notifies %+v", a)
	}
	a := got["e-ask"].Attention
	var titles []string
	for _, x := range a.Actions {
		titles = append(titles, x.Title)
	}
	if a.Title != "Pergunta" || !slices.Equal(titles[:3], []string{"Sim (sugerido)", "Não", "Responder"}) {
		t.Fatalf("the question notifies %q with %q", a.Title, titles)
	}
}
