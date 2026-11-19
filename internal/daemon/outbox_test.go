package daemon

import (
	"context"
	"fmt"
	"maps"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/plugins/bundled/bundledtest"
	"github.com/wloophq/wloop/internal/readmodel"
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

// roundKinds are the bundled conversation kinds, and a review round's review and resolve, posted as comments.
type roundKinds struct{ bundledtest.Shapes }

func (k roundKinds) Lookup(provider, kind string) (draft.KindSpec, bool) {
	if kind == "review" || kind == "resolve" {
		return draft.KindSpec{Provider: provider, Name: kind, Delivery: "comment", TextRequired: true, Editable: []string{draft.EditableText}}, true
	}
	return k.Shapes.Lookup(provider, kind)
}

func (k roundKinds) Shape(provider string, d draft.Draft) (draft.Draft, error) {
	if d.Kind == "review" || d.Kind == "resolve" {
		d.Purpose, d.Delivery = string(d.Kind), "comment"
		return d, nil
	}
	return k.Shapes.Shape(provider, d)
}

// withRows serves rows as the rows of their units; any other unit has none.
func (r *outboxRig) withRows(rows map[unit.Unit]api.Row) {
	r.s.readRows = func(_ context.Context, units []unit.Unit) (map[unit.Unit]readmodel.Read, error) {
		out := map[unit.Unit]readmodel.Read{}
		for _, u := range units {
			if row, ok := rows[u]; ok {
				out[u] = readmodel.Read{Row: row}
			}
		}
		return out, nil
	}
}

var slackThread = unit.Unit{Provider: "slack", Key: "C0ENG:1700000000.000100"}

func TestOutboxServesDraftFacets(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.withFakeWorkflows()
	r.o.Kinds = roundKinds{}
	github := r.o.Effects
	r.o.Effects = func(p unit.ProviderID) (port.Effects, error) {
		if p == slackThread.Provider {
			return r.fx, nil
		}
		return github(p)
	}
	r.withRows(map[unit.Unit]api.Row{r7u: {Facets: map[string][]string{"where": {"acme/api"}}}, slackThread: {Facets: map[string][]string{"where": {"#eng"}}}})
	turn := func(key string) draft.Origin {
		return draft.Origin{Kind: draft.OriginTurn, TurnSeq: 4, Intent: "review", Key: key}
	}
	review := r.proposeAs(t, outbox.Proposal{Kind: "review", Text: "Looks good overall", Origin: turn("r1")})
	resolve := r.proposeAs(t, outbox.Proposal{Kind: "resolve", Text: "Fixed in the last push", Origin: turn("r2")})
	nudge := r.proposeAs(t, outbox.Proposal{Unit: slackThread, Kind: "nudge", Text: "any news, @{slack:U0ALICE}?",
		Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "fake-nudge", Key: "n"}})

	page := r.outboxPage(t)
	want := map[int64]map[string][]string{
		int64(review.ID):  {"proposer": {"intent:review"}, "kind": {"review"}, "source": {"github"}, "where": {"acme/api"}, "state": {"held"}},
		int64(resolve.ID): {"proposer": {"intent:review"}, "kind": {"resolve"}, "source": {"github"}, "where": {"acme/api"}, "state": {"held"}},
		int64(nudge.ID): {"proposer": {"workflow:fake-nudge"}, "workflow": {"fake-nudge"}, "kind": {"nudge"}, "source": {"slack"}, "where": {"#eng"},
			"people": {"slack:U0ALICE"}, "state": {"held"}},
	}
	if len(page.Drafts) != len(want) {
		t.Fatalf("%d drafts, want %d", len(page.Drafts), len(want))
	}
	for _, d := range page.Drafts {
		got := maps.Clone(d.Facets)
		if len(got["age"]) != 1 {
			t.Errorf("draft %d's age %v, want when it was written", d.ID, got["age"])
		}
		delete(got, "age")
		if !reflect.DeepEqual(got, want[d.ID]) {
			t.Errorf("draft %d's facets\n %v\nwant\n %v", d.ID, got, want[d.ID])
		}
		if !slices.Contains(d.Texts, d.Text) || !slices.Contains(d.Texts, d.Label) || !slices.Contains(d.Texts, d.Facets["where"][0]) {
			t.Errorf("draft %d's search texts %q, want its text, label and where", d.ID, d.Texts)
		}
	}
	var ids []string
	for _, f := range page.Facets {
		ids = append(ids, f.ID)
	}
	if want := []string{"proposer", "workflow", "kind", "source", "where", "people", "state", "age"}; !slices.Equal(ids, want) {
		t.Errorf("facets %q, want %q", ids, want)
	}
}

func TestOutboxHeads(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.withFakeWorkflows()
	r.proposeAs(t, outbox.Proposal{Text: "any news?", Purpose: "nudge", Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "fake-nudge", Key: "a"}})
	r.proposeAs(t, outbox.Proposal{Text: "acking", Purpose: "nudge", Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "fake-ack", Key: "b"}})
	r.proposeAs(t, outbox.Proposal{Kind: "nudge", Text: "is this still moving?", Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "github-nudge", Key: "c"}})
	r.proposeAs(t, outbox.Proposal{Text: "thanks!", Origin: draft.Origin{Kind: draft.OriginUser, Key: "d"}})
	r.proposeAs(t, outbox.Proposal{Text: "one question", Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 2, Intent: "review", Key: "e"}})
	r.proposeAs(t, outbox.Proposal{Text: "from a session", Origin: draft.Origin{Kind: draft.OriginSession, Session: "s1", Key: "f"}})

	page := r.outboxPage(t)
	l := r.s.loaded()
	review, _ := l.Registry.Get("review")
	nudgeHead := api.OutboxHead{Title: "Ask who hasn't answered", About: "Drafted when a conversation where you asked something goes quiet.", Workflow: "fake-nudge"}
	want := map[string]map[string]api.OutboxHead{
		"proposer": {
			"workflow:fake-nudge": nudgeHead,
			"rule:github-nudge":   {Title: l.Gates.Drafts.Rules["github-nudge"]},
			"user":                {Title: l.Gates.Drafts.ByHand, About: l.Gates.Drafts.ByHandAbout},
			"session":             {Title: l.Gates.Drafts.BySession, About: l.Gates.Drafts.BySessionAbout},
			"intent:review":       {Title: review.Outbox.Title, About: review.Outbox.About},
		},
		"workflow": {"fake-nudge": nudgeHead},
		"kind":     {"reply": {Title: l.UI["github"].Drafts["reply"].Label}, "nudge": {Title: l.UI["github"].Drafts["nudge"].Label}},
	}
	if !reflect.DeepEqual(page.Heads, want) {
		t.Errorf("heads\n %+v\nwant\n %+v", page.Heads, want)
	}
	for _, h := range []api.OutboxHead{want["proposer"]["rule:github-nudge"], want["proposer"]["user"], want["proposer"]["session"], want["proposer"]["intent:review"], want["kind"]["reply"]} {
		if h.Title == "" {
			t.Fatalf("the bundled profile words none of %+v", want)
		}
	}
}

func TestOutboxDraftWithoutRow(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.withRows(nil)
	r.proposeAs(t, outbox.Proposal{Text: "hi", Origin: draft.Origin{Kind: draft.OriginUser, Key: "x"}})
	page := r.outboxPage(t)
	if len(page.Drafts) != 1 {
		t.Fatalf("%d drafts, want 1", len(page.Drafts))
	}
	if vs, ok := page.Drafts[0].Facets["where"]; ok {
		t.Errorf("a draft whose unit has no row is where %v, want no value", vs)
	}
	if got := page.Drafts[0].Facets["proposer"]; !slices.Equal(got, []string{"user"}) {
		t.Errorf("proposer %v, want user", got)
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
	if len(page.Drafts) != 1 {
		t.Fatalf("drafts %+v", page.Drafts)
	}
	got := page.Drafts[0]
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
}

func TestTheOutboxOfADraftWithNoTaskHasAnEmptyContext(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.proposeAs(t, outbox.Proposal{Text: "hi", Origin: draft.Origin{Kind: draft.OriginUser, Key: "x"}})
	page := r.outboxPage(t)
	c := page.Drafts[0].Context
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

func TestTheOutboxIsInItsViewsOrder(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	base := r.clock.Now()
	write := func(hours int, kind draft.Kind, key string) draft.ID {
		r.clock.set(base.Add(time.Duration(hours) * time.Hour))
		return r.proposeAs(t, outbox.Proposal{Kind: kind, Text: "draft " + key, Origin: draft.Origin{Kind: draft.OriginUser, Key: key}}).ID
	}
	late := write(2, "reply", "late")
	early := write(0, "nudge", "early")
	middle := write(1, "reply", "middle")
	order := func(path string) []draft.ID {
		t.Helper()
		var page api.OutboxPage
		if code := get(t, r.s, path, &page); code != 200 {
			t.Fatalf("GET %s: %d", path, code)
		}
		var ids []draft.ID
		for _, d := range page.Drafts {
			ids = append(ids, draft.ID(d.ID))
		}
		return ids
	}
	if got, want := order(api.OutboxPath), []draft.ID{early, middle, late}; !slices.Equal(got, want) {
		t.Errorf("bundled Outbox order %v, want the oldest first %v", got, want)
	}

	l := *r.s.loaded()
	l.Views.Views = slices.Clone(l.Views.Views)
	for i, v := range l.Views.Views {
		if v.ID == "outbox" {
			l.Views.Views[i].Sort = []intentload.SortKey{{Fact: "draft.created_at", Desc: true}}
			later := v
			later.ID, later.Sort = "send-later", []intentload.SortKey{{Fact: "draft.kind"}, {Fact: "draft.created_at"}}
			l.Views.Views = append(l.Views.Views, later)
			break
		}
	}
	r.s.deps.Intents = func() *intentload.Loaded { return &l }
	if got, want := order(api.OutboxPath), []draft.ID{late, middle, early}; !slices.Equal(got, want) {
		t.Errorf("Outbox order %v with its sort descending, want the newest first %v", got, want)
	}
	if got, want := order(api.OutboxPath+"?view=send-later"), []draft.ID{early, middle, late}; !slices.Equal(got, want) {
		t.Errorf("send-later order %v, want by kind then oldest first %v", got, want)
	}
}
