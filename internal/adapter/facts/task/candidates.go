package facttask

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// CandidateDeps are what task.candidates and task.link_target_tasks read.
type CandidateDeps struct {
	// From and To are the links from and to any of units, as the link store keeps them; nil: none.
	From, To func(ctx context.Context, units []unit.Unit) ([]link.Link, error)
	// Settings bound the candidates; nil lists none.
	Settings func() CandidateSettings
}

// CandidateSettings are tasks.candidate_window and tasks.candidate_cap.
type CandidateSettings struct {
	// Window is how far apart two tasks' creations may be for one to be the other's candidate; 0 bounds nothing.
	Window time.Duration
	// Cap is the most candidates a task lists; 0 lists none.
	Cap int
}

// The weight of each signal two tasks share in a candidate's rank.
const (
	weightCited  = 3
	weightTarget = 2
	weightPerson = 1
)

const (
	passCandidates = "facts.candidates"
	passLinks      = "facts.candidate_links"
)

// pool is the tasks a task's candidates are taken from, indexed by the units they hold and the people their
// items name.
type pool struct {
	tasks    []task.Task
	at       map[task.ID]int
	people   [][][]string
	byUnit   map[unit.Unit][]int
	byPerson map[string][]int
}

// graph is the links from and to the units of a pass's tasks.
type graph struct{ from, to map[unit.Unit][]link.Link }

// candidates is task.candidates of e, without refused and the tasks related to e: the open tasks that share
// with e a person their items name, a unit their links target, or a unit one of them holds and the other's
// links target, created within the window of e, the most shared first, then the closest in time, at most Cap.
func (p *Provider) candidates(ctx context.Context, e task.Task, refused []string) ([]string, error) {
	out := []string{}
	if p.d.Candidates.Settings == nil {
		return out, nil
	}
	s := p.d.Candidates.Settings()
	if s.Cap <= 0 || !p.mergeable(e) {
		return out, nil
	}
	refused = append(slices.Clone(refused), p.related(e)...)
	pl, err := p.candidatePool(ctx, e, s.Window)
	if err != nil {
		return nil, err
	}
	score := make([]int, len(pl.tasks))
	add := func(is []int, w int) {
		for _, i := range is {
			score[i] += w
		}
	}
	people, err := p.people(ctx, e, pl)
	if err != nil {
		return nil, err
	}
	for _, keys := range people {
		var is []int
		for _, k := range keys {
			is = append(is, pl.byPerson[k]...)
		}
		slices.Sort(is)
		add(slices.Compact(is), weightPerson)
	}
	if p.d.Candidates.From != nil && p.d.Candidates.To != nil {
		if err := p.linked(ctx, e, pl, add); err != nil {
			return nil, err
		}
	}
	before := func(a, b int) bool {
		return cmp.Or(cmp.Compare(score[b], score[a]), cmp.Compare(apart(e, pl.tasks[a]), apart(e, pl.tasks[b])),
			cmp.Compare(pl.tasks[a].ID, pl.tasks[b].ID)) < 0
	}
	top := make([]int, 0, s.Cap)
	for i, n := range score {
		o := pl.tasks[i]
		if n == 0 || o.ID == e.ID || !within(e, o, s.Window) || slices.Contains(refused, string(o.ID)) ||
			len(top) == s.Cap && !before(i, top[len(top)-1]) {
			continue
		}
		at := len(top)
		for at > 0 && before(i, top[at-1]) {
			at--
		}
		top = slices.Insert(top, at, i)[:min(len(top)+1, s.Cap)]
	}
	for _, i := range top {
		out = append(out, string(pl.tasks[i].ID))
	}
	return out, nil
}

// linked adds to the tasks of pl the weight of the links e shares with them: a unit e's links target that a task
// holds or also links, and a unit of e another task's links target.
func (p *Provider) linked(ctx context.Context, e task.Task, pl pool, add func([]int, int)) error {
	own := unitsOf(e)
	var targets []unit.Unit
	err := p.eachLink(ctx, own, true, func(l link.Link) {
		if cites(e, l) && !slices.Contains(own, l.To) && !slices.Contains(targets, l.To) {
			targets = append(targets, l.To)
		}
	})
	if err != nil {
		return err
	}
	for _, t := range targets {
		add(pl.byUnit[t], weightCited)
	}
	seen := map[[2]unit.Unit]bool{}
	return p.eachLink(ctx, append(slices.Clone(targets), own...), false, func(l link.Link) {
		if slices.Contains(own, l.From) || seen[[2]unit.Unit{l.From, l.To}] {
			return
		}
		seen[[2]unit.Unit{l.From, l.To}] = true
		if slices.Contains(own, l.To) {
			add(pl.byUnit[l.From], weightCited)
		} else {
			add(pl.byUnit[l.From], weightTarget)
		}
	})
}

// eachLink runs fn over the links from any of units, or to any of them when from is false. In a pass, the links
// from every unit of it are read once, and those are the only links to a unit a candidate holds.
func (p *Provider) eachLink(ctx context.Context, units []unit.Unit, from bool, fn func(link.Link)) error {
	g, ok, err := p.graph(ctx)
	if err != nil {
		return err
	}
	if ok {
		by := g.to
		if from {
			by = g.from
		}
		for _, u := range units {
			for _, l := range by[u] {
				fn(l)
			}
		}
		return nil
	}
	read := p.d.Candidates.To
	if from {
		read = p.d.Candidates.From
	}
	ls, err := read(ctx, units)
	for _, l := range ls {
		fn(l)
	}
	return err
}

func (p *Provider) graph(ctx context.Context) (graph, bool, error) {
	pass, ok := coretasks.PassOf(ctx)
	if !ok {
		return graph{}, false, nil
	}
	g, err := coretasks.Once(ctx, passLinks, func() (graph, error) {
		ls, err := p.d.Candidates.From(ctx, pass.Units())
		g := graph{from: map[unit.Unit][]link.Link{}, to: map[unit.Unit][]link.Link{}}
		for _, l := range ls {
			g.from[l.From] = append(g.from[l.From], l)
			g.to[l.To] = append(g.to[l.To], l)
		}
		return g, err
	})
	return g, true, err
}

// cites says whether link l is of e's: of its unit as a whole, of an item e holds, or of any item of a unit e
// holds none of.
func cites(e task.Task, l link.Link) bool {
	if l.Item == "" {
		return true
	}
	ref := task.ItemRef{Unit: l.From, Item: l.Item}
	if slices.Contains(e.Items, ref) || slices.Contains(e.Resolved, ref) {
		return true
	}
	return !slices.ContainsFunc(append(slices.Clone(e.Items), e.Resolved...), func(r task.ItemRef) bool { return r.Unit == l.From })
}

// candidatePool is the pool e's candidates come from: in a pass, every mergeable task of it, read once; else the
// stored open ones created within window of e.
func (p *Provider) candidatePool(ctx context.Context, e task.Task, window time.Duration) (pool, error) {
	if pass, ok := coretasks.PassOf(ctx); ok {
		return coretasks.Once(ctx, passCandidates, func() (pool, error) { return p.newPool(ctx, pass.Tasks) })
	}
	es, err := p.d.Tasks.Tasks(ctx, task.Query{})
	if err != nil {
		return pool{}, err
	}
	return p.newPool(ctx, slices.DeleteFunc(es, func(o task.Task) bool { return !within(e, o, window) }))
}

func (p *Provider) newPool(ctx context.Context, es []task.Task) (pool, error) {
	pl := pool{at: map[task.ID]int{}, byUnit: map[unit.Unit][]int{}, byPerson: map[string][]int{}}
	var units []unit.Unit
	for _, o := range es {
		if p.mergeable(o) {
			pl.at[o.ID] = len(pl.tasks)
			pl.tasks = append(pl.tasks, o)
			units = append(units, unitsOf(o)...)
		}
	}
	got, err := p.items(ctx, units)
	if err != nil {
		return pool{}, err
	}
	me := p.me()
	pl.people = make([][][]string, len(pl.tasks))
	for i, o := range pl.tasks {
		for _, u := range unitsOf(o) {
			pl.byUnit[u] = append(pl.byUnit[u], i)
		}
		pl.people[i] = peopleOf(o, got, me)
		for _, keys := range pl.people[i] {
			for _, k := range keys {
				pl.byPerson[k] = append(pl.byPerson[k], i)
			}
		}
	}
	return pl, nil
}

// people are the people e's items name, as peopleOf says; pl's when it holds e as it is.
func (p *Provider) people(ctx context.Context, e task.Task, pl pool) ([][]string, error) {
	if i, ok := pl.at[e.ID]; ok && pl.tasks[i].Updated.Equal(e.Updated) {
		return pl.people[i], nil
	}
	got, err := p.items(ctx, unitsOf(e))
	if err != nil {
		return nil, err
	}
	return peopleOf(e, got, p.me()), nil
}

// peopleOf are the people the requesters, assignees and waited-on of e's heldItems are, each as its keys and
// aliases, a key or alias two of them share making them one; never the user, a group or the loop.
func peopleOf(e task.Task, got map[unit.Unit][]items.Item, me task.Me) [][]string {
	var out [][]string
	addPerson := func(who items.Person) {
		if who.Key == "" || who.Is(me) || strings.HasPrefix(who.Key, "group:") || strings.HasPrefix(who.Key, "system:") {
			return
		}
		keys := append([]string{who.Key}, who.Aliases...)
		shares := func(g []string) bool {
			return slices.ContainsFunc(g, func(k string) bool { return slices.Contains(keys, k) })
		}
		for i := 0; i < len(out); {
			if !shares(out[i]) {
				i++
				continue
			}
			keys = append(keys, out[i]...)
			out = slices.Delete(out, i, i+1)
		}
		slices.Sort(keys)
		out = append(out, slices.Compact(keys))
	}
	for _, it := range heldItems(e, got) {
		addPerson(it.Requester)
		for _, who := range it.Assignees {
			addPerson(who)
		}
		for _, who := range it.WaitingOn {
			addPerson(who)
		}
	}
	return out
}

// heldItems are the items of got e holds, open or resolved, and every item of a unit of e it holds none of.
func heldItems(e task.Task, got map[unit.Unit][]items.Item) []items.Item {
	var out []items.Item
	refs := append(slices.Clone(e.Items), e.Resolved...)
	for _, u := range unitsOf(e) {
		holdsSome := slices.ContainsFunc(refs, func(r task.ItemRef) bool { return r.Unit == u })
		for _, it := range got[u] {
			if !holdsSome || slices.Contains(refs, task.ItemRef{Unit: u, Item: it.ID}) {
				out = append(out, it)
			}
		}
	}
	return out
}

// mergeable says whether e may be a candidate or have any: an open task of an enabled plugin, neither a project
// nor a draft's or a message's.
func (p *Provider) mergeable(e task.Task) bool {
	return p.listed(e) && !e.IsProject() && e.Origin != task.OriginDraft && e.Origin != task.OriginMessage
}

// related are the ids e's property of role related lists.
func (p *Provider) related(e task.Task) []string {
	if p.d.RoleProp == nil {
		return nil
	}
	name := p.d.RoleProp(task.RoleRelated)
	if name == "" {
		return nil
	}
	return e.Prop(name).List
}

func (p *Provider) me() task.Me {
	if p.d.Me == nil {
		return task.Me{}
	}
	return p.d.Me()
}

func apart(a, b task.Task) time.Duration {
	d := a.Created.Sub(b.Created)
	return max(d, -d)
}

func within(e, o task.Task, window time.Duration) bool { return window <= 0 || apart(e, o) <= window }
