package daemon

import (
	"context"
	"fmt"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// withFakeWorkflows makes r's profile the bundled one with fake-nudge and fake-close, two drafting workflows
// whose [outbox] words are their own.
func (r *outboxRig) withFakeWorkflows() {
	l := *bundledLoaded()
	l.Workflows = append(slices.Clone(l.Workflows),
		workflow.Workflow{Name: "fake-nudge", Summary: "Nudge a quiet conversation", Outbox: workflow.Outbox{Title: "Ask who hasn't answered", About: "Drafted when a conversation where you asked something goes quiet."}},
		workflow.Workflow{Name: "fake-close", Summary: "Close a finished conversation", Outbox: workflow.Outbox{Title: "Archive finished conversations"}})
	r.s.deps.Intents = func() *intentload.Loaded { return &l }
}

func (r *outboxRig) proposeAs(t *testing.T, p outbox.Proposal) draft.Draft {
	t.Helper()
	if p.Unit.Provider == "" {
		p.Unit = r7u
	}
	if p.Kind == "" && p.Purpose == "" {
		p.Kind = draft.Kind("reply")
	}
	p.Policy = draft.Fixed(draft.Approval{Mode: draft.Wait})
	d, err := r.o.Propose(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (r *outboxRig) outboxPage(t *testing.T) api.OutboxPage {
	t.Helper()
	var page api.OutboxPage
	if code := get(t, r.s, api.OutboxPath, &page); code != 200 {
		t.Fatalf("GET %s: %d", api.OutboxPath, code)
	}
	return page
}

func TestTheOutboxGroupsDraftsByTheirWorkflow(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.withFakeWorkflows()
	auto := func(wf, key string) draft.Origin {
		return draft.Origin{Kind: draft.OriginAuto, Workflow: wf, Key: key}
	}
	r.proposeAs(t, outbox.Proposal{Text: "any news?", Purpose: "nudge", Origin: auto("fake-nudge", "a")})
	r.proposeAs(t, outbox.Proposal{Unit: unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}, Text: "still there?", Purpose: "nudge", Origin: auto("fake-nudge", "b")})
	r.proposeAs(t, outbox.Proposal{Text: "closing this", Purpose: "close", Origin: auto("fake-close", "c")})
	r.proposeAs(t, outbox.Proposal{Text: "ping", Purpose: "nudge", Origin: draft.Origin{Kind: draft.OriginSession, Key: "d"}})
	r.proposeAs(t, outbox.Proposal{Text: "thanks!", Origin: draft.Origin{Kind: draft.OriginUser, Key: "e"}})

	page := r.outboxPage(t)
	var got []string
	for _, g := range page.Groups {
		got = append(got, fmt.Sprintf("%s|%s|%s|%s|%d", g.Workflow, g.Purpose, g.Title, g.About, len(g.Drafts)))
	}
	want := []string{
		"fake-nudge||Ask who hasn't answered|Drafted when a conversation where you asked something goes quiet.|2",
		"fake-close||Archive finished conversations||1",
		"|nudge|Nudge||1",
		"||Written by hand|Replies and reactions you wrote yourself, waiting to be sent.|1",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("groups\n %q\nwant\n %q", got, want)
	}
	if d := page.Groups[0].Drafts; d[0].Text != "still there?" || d[1].Text != "any news?" || d[0].Gate == 0 || d[0].Intents["approved"] == "" {
		t.Errorf("fake-nudge's drafts %+v, want both, newest first, with their gates", d)
	}
}

func TestTheOutboxCarriesEachDraftsContext(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.s.deps.TaskRecords = r.db
	ctx := context.Background()
	op := items.Op{Op: items.OpCreate, Unit: r7u, Item: "i1", Anchor: unit.Anchor{Ref: "user:i1"}, Fields: &items.Fields{Kind: ptrOf(items.Todo),
		Summary: ptrOf("Review the retry change"), Requester: &items.Person{Key: "github:dana", Name: "dana"}, WaitingOn: &[]items.Person{{Key: "github:me", Name: "me"}}}}
	if _, err := r.ledger.Apply(ctx, items.User, []items.Op{op}); err != nil {
		t.Fatal(err)
	}
	const id = task.ID("t-0000000000000ab1")
	r.put(t, task.Task{ID: id, State: "next", Category: task.CategoryReady, Title: "Retry flaky uploads", Units: []unit.Unit{r7u},
		Items: []task.ItemRef{{Unit: r7u, Item: "i1"}}})
	brief := `{"asks":[{"item":"i1","quotes":[{"text":"can you look?"}]}]}`
	if err := r.db.PutRecord(ctx, task.UnitOf(id), "brief", task.WriterPlugin, []byte(brief), tasksNow); err != nil {
		t.Fatal(err)
	}
	d := r.proposeAs(t, outbox.Proposal{Task: string(id), Items: []string{"i1"}, Text: "looking now", Purpose: "acknowledge",
		Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "fake-ack", Key: "k"}})

	page := r.outboxPage(t)
	if len(page.Groups) != 1 || len(page.Groups[0].Drafts) != 1 {
		t.Fatalf("groups %+v", page.Groups)
	}
	got := page.Groups[0].Drafts[0]
	if got.ID != int64(d.ID) || got.Task != string(id) {
		t.Fatalf("draft %+v", got.Draft)
	}
	asks := got.Context.Asks
	if len(asks) != 1 || asks[0].Item != "i1" || asks[0].Summary != "Review the retry change" || asks[0].Asker != "dana" || !slices.Equal(asks[0].Quotes, []string{"can you look?"}) {
		t.Errorf("asks %+v, want the task's ask with its quote", asks)
	}
	if its := got.Context.Items; len(its) != 1 || its[0] != (api.OutboxItem{ID: "i1", Summary: "Review the retry change", State: "open"}) {
		t.Errorf("items %+v, want the draft's item with its state", its)
	}
	if g := page.Groups[0]; g.Workflow != "fake-ack" || g.Title != "fake-ack" {
		t.Errorf("a workflow the profile lacks heads its group %q %q, want its name", g.Workflow, g.Title)
	}
}

func TestTheOutboxOfADraftWithNoTaskHasAnEmptyContext(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.proposeAs(t, outbox.Proposal{Text: "hi", Origin: draft.Origin{Kind: draft.OriginUser, Key: "x"}})
	page := r.outboxPage(t)
	c := page.Groups[0].Drafts[0].Context
	if c.Asks == nil || c.Items == nil || len(c.Asks)+len(c.Items) != 0 {
		t.Errorf("context %+v, want empty lists", c)
	}
}

// BenchmarkOutbox serves the Outbox of 200 drafts, each on its own task with a brief, over a store of 5000
// tasks.
func BenchmarkOutbox(b *testing.B) {
	ctx := context.Background()
	r := newOutboxRig(b)
	r.s.deps.TaskRecords = r.db
	var es []task.Task
	for i := range 5000 {
		u := unit.Unit{Provider: unit.GitHub, Key: fmt.Sprintf("acme/api#%d", i+1)}
		es = append(es, task.Task{ID: task.ID(fmt.Sprintf("t-%016x", i+1)), State: "next", Category: task.CategoryReady, Title: "Task " + strconv.Itoa(i),
			Units: []unit.Unit{u}, Items: []task.ItemRef{{Unit: u, Item: "i1"}}})
	}
	r.put(b, es...)
	for i := range 200 {
		e := es[i*25]
		op := items.Op{Op: items.OpCreate, Unit: e.Units[0], Item: "i1", Anchor: unit.Anchor{Ref: "user:i1"}, Fields: &items.Fields{Kind: ptrOf(items.Todo), Summary: ptrOf("Review it")}}
		if _, err := r.ledger.Apply(ctx, items.User, []items.Op{op}); err != nil {
			b.Fatal(err)
		}
		if err := r.db.PutRecord(ctx, task.UnitOf(e.ID), "brief", task.WriterPlugin, []byte(`{"asks":[{"item":"i1","quotes":[{"text":"can you look?"}]}]}`), tasksNow); err != nil {
			b.Fatal(err)
		}
		if _, err := r.o.Propose(ctx, outbox.Proposal{Unit: e.Units[0], Task: string(e.ID), Items: []string{"i1"}, Text: "any news?", Purpose: "nudge",
			Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: []string{"fake-nudge", "fake-close"}[i%2], Key: strconv.Itoa(i)},
			Policy: draft.Fixed(draft.Approval{Mode: draft.Wait})}); err != nil {
			b.Fatal(err)
		}
	}
	h := r.s.Handler()
	for b.Loop() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, apiRequest("GET", api.OutboxPath, nil))
		if rec.Code != 200 {
			b.Fatalf("GET: %d %s", rec.Code, rec.Body)
		}
	}
}
