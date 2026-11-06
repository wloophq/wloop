package facttask_test

import (
	"context"
	"slices"
	"testing"
	"time"

	facttask "github.com/wloophq/wloop/internal/adapter/facts/task"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/gates"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

const day = 24 * time.Hour

var (
	uma   = items.Person{Key: "chat:U1", Name: "Uma Um"}
	davi  = items.Person{Key: "chat:U2", Name: "Davi Dois"}
	tito  = items.Person{Key: "chat:U3", Name: "Tito Tres"}
	meP   = items.Person{Key: "chat:ME", Name: "Me"}
	loopP = items.Person{Key: "system:" + brand.Name, Name: brand.Name}
	teamP = items.Person{Key: "group:backend", Name: "backend"}
)

// whereCounting counts the gate queries over every task.
type whereCounting struct {
	sqlite.GateStore
	e *env
}

func (c whereCounting) Where(ctx context.Context, q gates.Query) ([]gate.Gate, error) {
	c.e.reads["gates where"]++
	return c.GateStore.Where(ctx, q)
}

// candidateEnv is env with a provider that reads the candidates and the refused merges.
type candidateEnv struct {
	*env
	p *facttask.Provider
	s facttask.CandidateSettings
	// aliases are the ids that read as another task's, as a merge leaves them.
	aliases map[task.ID]task.ID
}

type aliasedTasks struct {
	store   facttask.Tasks
	aliases map[task.ID]task.ID
}

func (a aliasedTasks) Tasks(ctx context.Context, q task.Query) ([]task.Task, error) {
	return a.store.Tasks(ctx, q)
}

func (a aliasedTasks) Task(ctx context.Context, id task.ID) (task.Task, error) {
	if to, ok := a.aliases[id]; ok {
		id = to
	}
	return a.store.Task(ctx, id)
}

func newCandidateEnv(t *testing.T) *candidateEnv {
	t.Helper()
	c := &candidateEnv{env: newEnv(t), s: facttask.CandidateSettings{Window: 14 * day, Cap: 5}, aliases: map[task.ID]task.ID{}}
	c.p = facttask.New(facttask.Deps{
		Tasks: aliasedTasks{c.db.Tasks(), c.aliases}, Items: c.db.Items(), Clock: c.clock,
		Me:    func() task.Me { return task.Me{Keys: []string{meP.Key}} },
		Gates: whereCounting{c.db.Gates(func() time.Time { return c.clock.now }), c.env},
		Candidates: facttask.CandidateDeps{
			From: func(ctx context.Context, us []unit.Unit) ([]link.Link, error) {
				c.reads["links"]++
				return c.db.LinksFrom(ctx, us)
			},
			To: func(ctx context.Context, us []unit.Unit) ([]link.Link, error) {
				c.reads["links"]++
				return c.db.Links().To(ctx, us)
			},
			Settings: func() facttask.CandidateSettings { return c.s },
		},
		Refusals: facttask.RefusalDeps{Merges: func(v task.Verb) bool { return v == task.VerbMerge }},
		RoleProp: func(r task.Role) string {
			name, _ := bundledTasks.PropOf(r)
			return name
		},
	})
	return c
}

// open is an open task on u holding its, created at.
func (c *candidateEnv) open(id task.ID, u unit.Unit, created time.Time, its ...items.Item) task.Task {
	c.t.Helper()
	en := task.Task{ID: id, State: "next", Category: task.CategoryReady, Title: "do " + string(id), Units: []unit.Unit{u},
		Created: created, Updated: created}
	for _, it := range its {
		en.Items = append(en.Items, task.ItemRef{Unit: it.Unit, Item: it.ID})
	}
	return c.task(en)
}

// ask is an open item id on u that requester asks of the waited-on.
func (c *candidateEnv) ask(u unit.Unit, id string, requester items.Person, waitingOn ...items.Person) items.Item {
	c.t.Helper()
	it := items.Item{ID: id, Unit: u, Kind: items.Request, Summary: "s " + id, Origin: unit.Anchor{Unit: u, Ref: "m:" + id},
		Requester: requester, WaitingOn: waitingOn, Basis: items.NoBasis, State: items.Open, Confidence: items.High,
		Provenance: items.Extracted, CreatedAt: monday, UpdatedAt: monday}
	if err := c.db.Items().Commit(context.Background(), coreitems.Commit{Items: []items.Item{it}}); err != nil {
		c.t.Fatal(err)
	}
	return it
}

func (c *candidateEnv) link(from unit.Unit, item string, to unit.Unit, kind link.Kind) {
	c.t.Helper()
	l := link.Link{From: from, To: to, Kind: kind, Item: item, Evidence: unit.Anchor{Unit: from, Ref: "m:" + item}, Origin: link.Extraction, At: monday}
	if err := c.db.Links().Put(context.Background(), l); err != nil {
		c.t.Fatal(err)
	}
}

func (c *candidateEnv) list(ctx context.Context, id task.ID, fact string) []string {
	c.t.Helper()
	f, err := c.p.Facts(workflow.WithReads(ctx, fact), task.UnitOf(id))
	if err != nil {
		c.t.Fatalf("facts of %s: %v", id, err)
	}
	got, ok := f[fact].([]string)
	if !ok || got == nil {
		c.t.Fatalf("%s of %s = %#v, want a list", fact, id, f[fact])
	}
	return got
}

// candidates are task.candidates of id, read alone and in a pass, which must agree.
func (c *candidateEnv) candidates(id task.ID) []string {
	c.t.Helper()
	alone := c.list(context.Background(), id, "task.candidates")
	var inPass []string
	if err := c.p.Prepare(context.Background(), func(ctx context.Context) error {
		inPass = c.list(ctx, id, "task.candidates")
		return nil
	}); err != nil {
		c.t.Fatal(err)
	}
	if !slices.Equal(alone, inPass) {
		c.t.Fatalf("candidates of %s: %v alone, %v in a pass", id, alone, inPass)
	}
	return alone
}

func chat(key string) unit.Unit { return unit.Unit{Provider: "chat", Key: key} }

func TestCandidatesShareAPersonALinkTargetOrACitedUnit(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	doc := chat("T1/C9/900.9")
	a := c.ask(chat("T1/C1/1.1"), "a1", uma, meP)
	c.link(a.Unit, "a1", pr, link.Review)
	c.link(a.Unit, "a1", doc, link.Mention)
	c.open("t-a", a.Unit, monday, a)
	c.open("t-person", chat("T1/C2/2.2"), monday.Add(-4*day), c.ask(chat("T1/C2/2.2"), "b1", uma, meP))
	c.open("t-pr", pr, monday.Add(-day))
	d := c.ask(chat("T1/C4/4.4"), "d1", tito, meP)
	c.link(d.Unit, "d1", doc, link.Mention)
	c.open("t-target", d.Unit, monday.Add(-day))
	cit := c.ask(chat("T1/C5/5.5"), "e1", davi, meP)
	c.link(cit.Unit, "e1", a.Unit, link.Mention)
	c.open("t-cites", cit.Unit, monday.Add(-2*day), cit)
	c.open("t-me-only", chat("T1/C6/6.6"), monday, c.ask(chat("T1/C6/6.6"), "f1", items.Person{Key: "chat:U4", Name: "Vera Quatro"}, meP))
	done := c.open("t-done", chat("T1/C7/7.7"), monday, c.ask(chat("T1/C7/7.7"), "g1", uma, meP))
	done.State, done.Category = "done", task.CategoryDone
	c.task(done)
	project := c.open("t-project", chat("T1/C8/8.8"), monday, c.ask(chat("T1/C8/8.8"), "h1", uma, meP))
	project.Props = task.Props{task.PropKind: {Text: task.KindProject}}
	c.task(project)

	if got, want := c.candidates("t-a"), []string{"t-pr", "t-cites", "t-target", "t-person"}; !slices.Equal(got, want) {
		t.Errorf("candidates of t-a %v, want %v", got, want)
	}
	for id, want := range map[task.ID][]string{"t-person": {"t-a"}, "t-pr": {"t-a"}, "t-target": {"t-a"}, "t-cites": {"t-a"}} {
		if got := c.candidates(id); !slices.Equal(got, want) {
			t.Errorf("candidates of %s %v, want %v", id, got, want)
		}
	}
}

func TestCandidatesStayWithinTheWindowAndTheCap(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	c.s.Cap = 2
	c.open("t-a", chat("T1/C1/1.1"), monday, c.ask(chat("T1/C1/1.1"), "a1", uma, meP))
	for id, at := range map[task.ID]time.Time{"t-1d": monday.Add(-day), "t-3d": monday.Add(-3 * day), "t-2d": monday.Add(2 * day), "t-20d": monday.Add(-20 * day)} {
		u := chat("T1/C2/" + string(id))
		c.open(id, u, at, c.ask(u, "b1", uma, meP))
	}
	if got, want := c.candidates("t-a"), []string{"t-1d", "t-2d"}; !slices.Equal(got, want) {
		t.Errorf("candidates of t-a %v, want the two closest in time %v", got, want)
	}
	if got := c.candidates("t-20d"); len(got) != 0 {
		t.Errorf("candidates of a task 20 days apart from the rest %v, want none", got)
	}
	c.s.Window = 0
	if got, want := c.candidates("t-20d"), []string{"t-3d", "t-1d"}; !slices.Equal(got, want) {
		t.Errorf("candidates with no window %v, want %v", got, want)
	}
}

func TestNoCandidatesWhenNothingIsShared(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	c.open("t-a", chat("T1/C1/1.1"), monday, c.ask(chat("T1/C1/1.1"), "a1", loopP, meP, teamP))
	c.open("t-b", chat("T1/C2/2.2"), monday, c.ask(chat("T1/C2/2.2"), "b1", loopP, meP, teamP))
	c.open("t-c", chat("T1/C3/3.3"), monday, c.ask(chat("T1/C3/3.3"), "c1", davi, meP))
	if got := c.candidates("t-a"); len(got) != 0 {
		t.Errorf("candidates sharing only the user, the loop and a group %v, want none", got)
	}
	c.open("t-d", chat("T1/C4/4.4"), monday, c.ask(chat("T1/C4/4.4"), "d1", davi, meP))
	c.s.Cap = 0
	if got := c.candidates("t-c"); len(got) != 0 {
		t.Errorf("candidates with a cap of 0 %v, want none", got)
	}
}

// reject opens on a the merge gate proposing b, and rejects it.
func (c *candidateEnv) reject(a, b task.ID, verb task.Verb) gate.Gate {
	c.t.Helper()
	return c.resolve(a, b, verb, gate.Rejected)
}

func (c *candidateEnv) resolve(a, b task.ID, verb task.Verb, s gate.Status) gate.Gate {
	c.t.Helper()
	ctx := context.Background()
	gs := c.db.Gates(func() time.Time { return c.clock.now })
	g, err := gs.Open(ctx, gate.Gate{Task: a, Unit: task.UnitOf(a), Kind: gate.KindProposal, Name: string(verb), Status: gate.Open,
		Subject: `{"verb":"` + string(verb) + `","args":{"task":"` + string(b) + `"}}`, Intents: []string{"approve", "reject"}})
	if err != nil {
		c.t.Fatal(err)
	}
	g, err = gs.Resolve(ctx, g.ID, gatesResolution(s))
	if err != nil {
		c.t.Fatal(err)
	}
	return g
}

func TestCandidatesLeaveARefusedPairOut(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	c.open("t-a", chat("T1/C1/1.1"), monday.Add(-time.Hour), c.ask(chat("T1/C1/1.1"), "a1", uma, meP))
	c.open("t-b", chat("T1/C2/2.2"), monday.Add(-time.Hour), c.ask(chat("T1/C2/2.2"), "b1", uma, meP))
	c.open("t-c", chat("T1/C3/3.3"), monday.Add(-2*time.Hour), c.ask(chat("T1/C3/3.3"), "c1", uma, meP))
	c.reject("t-a", "t-b", task.VerbMerge)
	if got, want := c.candidates("t-a"), []string{"t-c"}; !slices.Equal(got, want) {
		t.Errorf("candidates of t-a %v, want %v without the refused t-b", got, want)
	}
	if got, want := c.candidates("t-b"), []string{"t-c"}; !slices.Equal(got, want) {
		t.Errorf("candidates of t-b %v, want %v without the refused t-a", got, want)
	}
}

func TestACostlyFactIsReadOnlyForAReaderThatNamesIt(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	a := c.ask(chat("T1/C1/1.1"), "a1", uma, meP)
	c.link(a.Unit, "a1", unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}, link.Review)
	c.open("t-a", a.Unit, monday, a)
	c.open("t-b", chat("T1/C2/2.2"), monday, c.ask(chat("T1/C2/2.2"), "b1", uma, meP))
	c.reject("t-b", "t-x", task.VerbMerge)
	holds := catalog.Holds(func() *intentload.Loaded { return &intentload.Loaded{Tasks: bundledTasks} }, c.p.TaskFacts)
	e, err := c.db.Tasks().Task(context.Background(), "t-a")
	if err != nil {
		t.Fatal(err)
	}
	clear(c.reads)
	if ok, err := holds(context.Background(), wake.Wake{Task: "t-a", When: `task.state == "next"`}, e); err != nil || !ok {
		t.Fatalf("the wake's when: %v %v", ok, err)
	}
	if c.reads["links"] != 0 || c.reads["gates where"] != 0 {
		t.Fatalf("a wake reading task.state read %v, want no links and no gate query", c.reads)
	}
	f, err := c.p.Facts(context.Background(), task.UnitOf("t-a"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"task.candidates", "task.refused_merges", "task.link_target_tasks"} {
		if got, ok := f[name].([]string); !ok || got == nil || len(got) != 0 {
			t.Errorf("%s read by no one = %#v, want the empty list", name, f[name])
		}
	}
	if ok, err := holds(context.Background(), wake.Wake{Task: "t-a", When: `"t-b" in task.candidates`}, e); err != nil || !ok {
		t.Fatalf("a wake naming task.candidates: %v %v, want it computed", ok, err)
	}
	ctx := workflow.WithReads(context.Background(), `{{index .task.candidates 0}}`)
	if f, err := c.p.Facts(ctx, task.UnitOf("t-a")); err != nil || !slices.Equal(f["task.candidates"].([]string), []string{"t-b"}) {
		t.Fatalf("a template naming task.candidates read %v %v", f["task.candidates"], err)
	}
}

func TestCandidatesLeaveARelatedTaskOut(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	related, _ := bundledTasks.PropOf(task.RoleRelated)
	for _, id := range []task.ID{"t-a", "t-b", "t-c"} {
		u := chat("T1/C1/" + string(id))
		c.open(id, u, monday, c.ask(u, "i1", uma, meP))
	}
	for id, other := range map[task.ID]string{"t-a": "t-b", "t-b": "t-a"} {
		e, err := c.db.Tasks().Task(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		e.Props = task.Props{related: {List: []string{other}}}
		c.task(e)
	}
	for id, want := range map[task.ID][]string{"t-a": {"t-c"}, "t-b": {"t-c"}} {
		if got := c.candidates(id); !slices.Equal(got, want) {
			t.Errorf("candidates of %s %v, want %v without the related task", id, got, want)
		}
	}
}

func TestOneSharedPersonWithAliasesWeighsBelowACitedUnit(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	vera := items.Person{Key: "chat:U7", Name: "Vera Sete", Aliases: []string{"email:vera@example.test", "gh:vera"}}
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}
	a := c.ask(chat("T1/C1/1.1"), "a1", vera, meP)
	c.link(a.Unit, "a1", pr, link.Review)
	c.open("t-a", a.Unit, monday, a)
	c.open("t-vera", chat("T1/C2/2.2"), monday, c.ask(chat("T1/C2/2.2"), "b1", vera, meP))
	c.open("t-pr", pr, monday.Add(-3*day))
	if got, want := c.candidates("t-a"), []string{"t-pr", "t-vera"}; !slices.Equal(got, want) {
		t.Errorf("candidates of t-a %v, want %v: one person counts once whatever their aliases", got, want)
	}
}
