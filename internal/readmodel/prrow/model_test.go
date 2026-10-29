package prrow_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/task"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
	"github.com/wloophq/wloop/internal/domain/workday"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"
	"github.com/wloophq/wloop/internal/situation"
	"github.com/wloophq/wloop/internal/turn"
)

const viewer = "user-01"

// rig is a read model over a seeded store, with a clock the test moves and
// a pending set it changes.
type rig struct {
	t      testing.TB
	f      *storetest.Fixture
	m      *readmodel.Model
	loaded *intentload.Loaded
	cfg    config.Config

	mu      sync.Mutex
	now     time.Time
	pending map[unit.Unit]overlay.Pending
	// agents are what the workspace snapshot last read.
	agents   []turns.AgentObservation
	agentsOK bool

	listed atomic.Int64
	// reports are what the rows read the plugin's reports from; nil
	// derives them from the inventory at each read.
	reports readmodel.Reports
}

func newRig(t testing.TB, f *storetest.Fixture) *rig {
	t.Helper()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, f: f, loaded: l, cfg: config.Defaults(), now: f.Now, pending: map[unit.Unit]overlay.Pending{}, agentsOK: true}
	r.agents = r.every(dturn.AgentWaiting)
	r.m = r.model()
	return r
}

// keepReports keeps the plugin's reports of the inventory in the store, as
// the host keeps a running plugin's, and has the rows read them there.
func (r *rig) keepReports() {
	r.t.Helper()
	snaps := psource.NewSnapshots(r.f.DB.Snapshots())
	if err := (ghreporttest.Inventory{Store: r.f.Inventory, Now: r.clock}).Keep(context.Background(), snaps); err != nil {
		r.t.Fatal(err)
	}
	r.reports = snaps
	r.m = r.model()
}

// model is a new read model over the rig's sources, as a restart builds.
func (r *rig) model() *readmodel.Model {
	var reports readmodel.Reports = ghreporttest.Inventory{Store: r.f.Inventory, Now: r.clock}
	if r.reports != nil {
		reports = r.reports
	}
	src := prrow.Source{Store: r.f.Inventory, Reports: reports, Briefs: brief.New(brief.Deps{Store: r.f.Inventory, PRs: inventorytest.Of(r.f.Inventory)}),
		Mutes: readmodel.WakeMutes(r.f.DB.Wakes().UnitWakes),
		RowDeps: readmodel.RowDeps{Books: r.f.Turns, Sessions: r.f.Turns, Activity: r.f.DB, Intakes: r.f.DB.Intakes().Open, Intake: intakeView,
			Settings: func() readmodel.RowSettings { return readmodel.RowSettings{Cfg: r.cfg, Loaded: r.loaded, GateOn: true} }}}
	m := readmodel.New(r.f.Log, r.f.DB, r, readmodel.AgentsFunc(r.lastAgents), r, counting{prrow.New(src), &r.listed})
	m.UseClock(r.clock)
	return m
}

func (r *rig) lastAgents() ([]turns.AgentObservation, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.agents), r.agentsOK
}

// every is an agent in state on each open session's pane.
func (r *rig) every(state dturn.AgentState) []turns.AgentObservation {
	open, err := r.f.Turns.Sessions(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	out := make([]turns.AgentObservation, 0, len(open))
	for _, s := range open {
		out = append(out, turns.AgentObservation{WorkspaceID: s.WorkspaceID, Pane: s.Pane, State: state})
	}
	return out
}

// snapshot sets what the workspace snapshot holds, without noting it.
func (r *rig) snapshot(ok bool, agents []turns.AgentObservation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents, r.agentsOK = agents, ok
}

func intakeView(i intake.Intake, b intake.BriefState, _ func() (int, error)) api.Intake {
	ref, _ := pr.ParseKey(i.Unit.Key)
	w := api.Intake{Seq: i.Seq, PR: ref.URL(), Intent: string(i.Intent), Source: i.Source, State: string(i.State), Attempt: 1}
	if b.Found {
		w.Attempt, w.LastError = b.Failures+1, b.LastError
	}
	return w
}

// openIntakes accepts an open intake on each of refs.
func (r *rig) openIntakes(refs ...pr.Ref) {
	r.t.Helper()
	for _, ref := range refs {
		if _, _, err := r.f.DB.Intakes().Accept(context.Background(), intake.Intake{Unit: ref.Unit(), Intent: "review", Source: "cli", State: intake.Accepted, AcceptedAt: r.f.Now}); err != nil {
			r.t.Fatal(err)
		}
	}
}

func (r *rig) clock() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

func (r *rig) advance(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = r.now.Add(d)
}

func (r *rig) Plans(context.Context) (map[unit.Unit]overlay.Pending, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.pending), nil
}

func (r *rig) propose(u unit.Unit, p overlay.Pending) {
	r.mu.Lock()
	r.pending[u] = p
	r.mu.Unlock()
	r.f.Log.Note(port.TopicPending, u)
}

func (r *rig) Settings(context.Context) (readmodel.Base, error) {
	return readmodel.Base{Viewers: map[unit.ProviderID]string{unit.GitHub: viewer}, Specs: catalog.Specs(r.loaded)}, nil
}

// counting counts the rebuilds a provider sees.
type counting struct {
	p *prrow.Provider
	n *atomic.Int64
}

func (c counting) Provider() unit.ProviderID { return c.p.Provider() }

func (c counting) Listed(ctx context.Context) ([]unit.Unit, error) {
	c.n.Add(1)
	return c.p.Listed(ctx)
}

func (c counting) Load(ctx context.Context, units []unit.Unit) (map[unit.Unit]any, error) {
	return c.p.Load(ctx, units)
}

func (c counting) Derive(base readmodel.Base, in any, at time.Time) (api.Row, error) {
	return c.p.Derive(base, in, at)
}

func (r *rig) rows(m *readmodel.Model, since api.Since) api.RowsPage {
	r.t.Helper()
	p, err := m.Rows(context.Background(), since)
	if err != nil {
		r.t.Fatal(err)
	}
	return p
}

// encoded is every row of a full page by unit, as JSON.
func encoded(t testing.TB, rows []api.Row) map[unit.Unit]string {
	t.Helper()
	out := make(map[unit.Unit]string, len(rows))
	for _, row := range rows {
		raw, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		out[row.Unit] = string(raw)
	}
	return out
}

// matchesFresh fails t unless the rig's rows equal those a new model derives
// from the store now.
func (r *rig) matchesFresh(got map[unit.Unit]string) {
	r.t.Helper()
	want := encoded(r.t, r.rows(r.model(), api.Since{}).Rows)
	if len(got) != len(want) {
		r.t.Fatalf("%d rows, a fresh derive has %d", len(got), len(want))
	}
	for u, w := range want {
		if got[u] != w {
			r.t.Fatalf("%s differs from a fresh derive:\n got %s\nwant %s", u, got[u], w)
		}
	}
}

// client merges pages of one view as a client does.
type client struct {
	since   api.Since
	rows    map[unit.Unit]api.Row
	removed int // units a delta removed
}

func (c *client) pull(r *rig) { c.apply(r.t, r.rows(r.m, c.since)) }

func (c *client) apply(t testing.TB, p api.RowsPage) {
	t.Helper()
	if p.Full {
		c.rows = map[unit.Unit]api.Row{}
	} else if p.Epoch != c.since.Epoch {
		t.Fatalf("a delta of epoch %s after %s", p.Epoch, c.since.Epoch)
	}
	for _, row := range p.Rows {
		c.rows[row.Unit] = row
	}
	for _, u := range p.Removed {
		if _, ok := c.rows[u]; ok {
			c.removed++
		}
		delete(c.rows, u)
	}
	c.since = api.Since{Epoch: p.Epoch, Rev: p.Rev}
}

func (c *client) all() []api.Row { return slices.Collect(maps.Values(c.rows)) }

func sqlLimit() metertest.Limit { return metertest.Limit{Res: meter.SQL} }

func TestRowsWithNoChangeIssueNoQuery(t *testing.T) {
	r := newRig(t, storetest.Seeded(t, storetest.Live))
	first := r.rows(r.m, api.Since{})
	ctx, sc := metertest.Start(t, context.Background())
	best := time.Duration(math.MaxInt64)
	for range 10 {
		start := time.Now()
		again, err := r.m.Rows(ctx, api.Since{})
		best = min(best, time.Since(start))
		if err != nil {
			t.Fatal(err)
		}
		if again.Rev != first.Rev || len(again.Rows) != len(first.Rows) {
			t.Fatalf("rev %d rows %d, then rev %d rows %d", first.Rev, len(first.Rows), again.Rev, len(again.Rows))
		}
	}
	metertest.Within(t, sc, metertest.Budget{sqlLimit(): 0, {Res: meter.Tx}: 0})
	if !race && best > 2*time.Millisecond {
		t.Errorf("the fastest of 10 reads with nothing noted took %s, want under 2ms", best)
	}
}

// rebuildSQL is one rebuild at live size: one statement per input, the
// reported units, their reports, their overrides and their tasks' wakes
// apart.
const rebuildSQL = 12

func TestRebuildAtLiveSize(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	r := newRig(t, f)
	r.keepReports()
	r.openIntakes(f.Open[1], f.Open[2])
	before := r.rows(r.m, api.Since{})
	withIntake := 0
	for _, row := range before.Rows {
		if p, _ := prrow.PayloadOf(row); p.Intake != nil {
			withIntake++
		}
	}
	if withIntake != 2 {
		t.Fatalf("%d rows show an intake, want 2", withIntake)
	}
	units := make([]unit.Unit, len(f.Open))
	for i, ref := range f.Open {
		units[i] = ref.Unit()
	}
	f.Log.Note(port.TopicPRs, units...)
	ctx, sc := metertest.Start(t, context.Background())
	after, err := r.m.Rows(ctx, api.Since{})
	if err != nil {
		t.Fatal(err)
	}
	metertest.Within(t, sc, metertest.Budget{sqlLimit(): rebuildSQL, {Res: meter.Tx, Op: "read"}: 1, {Res: meter.Workspace}: 0})
	t.Logf("rebuild: %d SQL\n%s", metertest.Count(sc, sqlLimit()), metertest.Report(sc.Tally()))
	if after.Rev != before.Rev {
		t.Errorf("a note that changed no row moved Rev from %d to %d", before.Rev, after.Rev)
	}
	if race {
		return
	}
	// Under load most rebuilds stall and the fastest stays near the
	// unloaded cost, so the budget catches a tenfold regression without
	// failing on a busy machine.
	best := time.Duration(math.MaxInt64)
	for range 10 {
		f.Log.Note(port.TopicPRs, units...)
		start := time.Now()
		if _, err := r.m.Rows(context.Background(), api.Since{}); err != nil {
			t.Fatal(err)
		}
		best = min(best, time.Since(start))
	}
	t.Logf("fastest rebuild: %s", best)
	if best > 200*time.Millisecond {
		t.Errorf("a rebuild at live size took %s, want under 200ms", best)
	}
}

func BenchmarkRebuildAtLiveSize(b *testing.B) {
	f := storetest.Seeded(b, storetest.Live)
	m := newRig(b, f).m
	units := make([]unit.Unit, len(f.Open))
	for i, ref := range f.Open {
		units[i] = ref.Unit()
	}
	ctx := context.Background()
	for b.Loop() {
		f.Log.Note(port.TopicPRs, units...)
		if _, err := m.Rows(ctx, api.Since{}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSnapshotNoteFlipsWaiting(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	r := newRig(t, f)
	r.snapshot(true, r.every(dturn.AgentWorking))
	working := kinds(r.rows(r.m, api.Since{}).Rows)[api.Kind(situation.KindLoopWaiting)]

	r.snapshot(true, r.every(dturn.AgentWaiting))
	if got := kinds(r.rows(r.m, api.Since{}).Rows)[api.Kind(situation.KindLoopWaiting)]; got != working {
		t.Fatalf("a snapshot nobody noted moved the waiting rows from %d to %d", working, got)
	}
	f.Log.Note(port.TopicAgents, f.WithTurns...)
	ctx, sc := metertest.Start(t, context.Background())
	page, err := r.m.Rows(ctx, api.Since{})
	if err != nil {
		t.Fatal(err)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.Workspace}: 0})
	waiting := kinds(page.Rows)[api.Kind(situation.KindLoopWaiting)]
	if waiting <= working {
		t.Fatalf("an agents note left %d rows waiting, %d before the agents asked", waiting, working)
	}
	r.matchesFresh(encoded(t, page.Rows))
}

func TestUnknownAgentsTellNoSessionWaiting(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	r := newRig(t, f)
	r.snapshot(true, r.every(dturn.AgentWorking))
	working := kinds(r.rows(r.m, api.Since{}).Rows)[api.Kind(situation.KindLoopWaiting)]
	r.snapshot(false, nil)
	f.Log.NoteAll(port.TopicAgents)
	if got := kinds(r.rows(r.m, api.Since{}).Rows)[api.Kind(situation.KindLoopWaiting)]; got != working {
		t.Fatalf("unknown agents left %d rows waiting, want the %d of no agent waiting", got, working)
	}
}

func kinds(rows []api.Row) map[api.Kind]int {
	out := map[api.Kind]int{}
	for _, row := range rows {
		out[row.Kind]++
	}
	return out
}

// patience bounds a wait for an event; it only matters when the event
// never comes, so it covers a machine under heavy load.
const patience = time.Minute

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(patience)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("no %s within %s", what, patience)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestRebuildPublishesOnlyChangedRows waits for the rebuild of a note that
// changes no row, so a publish from it would be the first change read.
func TestRebuildPublishesOnlyChangedRows(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	r := newRig(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes := make(chan api.Change, 16)
	done := make(chan struct{})
	go func() {
		r.m.Run(ctx, func(c api.Change) { changes <- c })
		close(done)
	}()
	defer func() { cancel(); <-done }()
	start := r.rows(r.m, api.Since{})

	units := make([]unit.Unit, len(f.Open))
	for i, ref := range f.Open {
		units[i] = ref.Unit()
	}
	rebuilds := r.listed.Load()
	f.Log.Note(port.TopicPRs, units...)
	eventually(t, "the note's rebuild", func() bool { return r.listed.Load() > rebuilds })

	ref := f.Open[3]
	if err := inventorytest.Of(f.Inventory).SetOverride(context.Background(), dinv.Override{PR: ref, Hold: true}); err != nil {
		t.Fatal(err)
	}
	var c api.Change
	select {
	case c = <-changes:
	case <-time.After(patience):
		t.Fatal("a held pull request published nothing")
	}
	if c.Type != "changed" || c.Epoch != start.Epoch || c.Rev != start.Rev+1 || c.All || !slices.Equal(c.Units, []unit.Unit{ref.Unit()}) {
		t.Fatalf("change %+v after rev %d", c, start.Rev)
	}
	delta := r.rows(r.m, api.Since{Epoch: start.Epoch, Rev: start.Rev})
	if delta.Full || len(delta.Rows) != 1 || delta.Rows[0].Unit != ref.Unit() || !overrideOf(delta.Rows[0]).Hold || len(delta.Removed) != 0 {
		t.Fatalf("delta %+v", delta)
	}
}

func TestReadYourWrites(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	r := newRig(t, f)
	r.rows(r.m, api.Since{})
	ref := f.Open[5]
	until := f.Now.Add(48 * time.Hour)
	if err := mute(context.Background(), f, ref, until); err != nil {
		t.Fatal(err)
	}
	row, ok, err := r.m.Row(context.Background(), ref.Unit(), catalog.Asked{})
	if p, _ := prrow.PayloadOf(row); err != nil || !ok || row.Kind != api.Kind(situation.KindMuted) || p.Mute == nil || !p.Mute.Until.Equal(until) {
		t.Fatalf("row %+v, ok %v, err %v", row, ok, err)
	}
}

func TestConcurrentReadersShareOneRebuild(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	r := newRig(t, f)
	r.rows(r.m, api.Since{})
	if err := inventorytest.Of(f.Inventory).SetOverride(context.Background(), dinv.Override{PR: f.Open[0], Hold: true}); err != nil {
		t.Fatal(err)
	}
	before := r.listed.Load()
	var wg sync.WaitGroup
	revs := make([]uint64, 9)
	for i := range revs {
		wg.Go(func() {
			p, err := r.m.Rows(context.Background(), api.Since{})
			if err != nil {
				t.Error(err)
			}
			revs[i] = p.Rev
		})
	}
	wg.Wait()
	if n := r.listed.Load() - before; n != 1 {
		t.Errorf("9 readers after one note rebuilt %d times, want 1", n)
	}
	slices.Sort(revs)
	if len(slices.Compact(revs)) != 1 {
		t.Errorf("readers saw revisions %v", revs)
	}
}

// mute mutes ref until a time: a wake on a task that holds it.
func mute(ctx context.Context, f *storetest.Fixture, ref pr.Ref, until time.Time) error {
	id := task.ID("t-" + strings.ReplaceAll(ref.String(), "/", "-"))
	e := task.Task{ID: id, State: "scheduled", Category: task.CategoryDeferred, Units: []unit.Unit{ref.Unit()}, Origin: task.OriginUser,
		Created: f.Now, Updated: f.Now}
	if err := f.DB.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
		return err
	}
	return f.DB.Wakes().Put(ctx, wake.Wake{Task: id, Until: until, Return: "next"})
}

// writer makes the n-th scripted write of a script over the seeded open
// pull requests; the unlinked ones from index 58 lapse and come back.
func writer(r *rig, n int) error {
	ctx := context.Background()
	f := r.f
	ref := f.Open[(n*7)%len(f.Open)]
	at := f.Now.Add(time.Duration(n) * time.Second)
	switch n % 9 {
	case 0:
		return mute(ctx, f, ref, f.Now.Add(time.Duration(1+n%3)*time.Hour))
	case 1:
		_, err := f.Inventory.AppendEvents(ctx, []dinv.Event{{PR: ref, Kind: dinv.KindComment, Key: fmt.Sprintf("script-%d", n), Actor: viewer, At: at}})
		return err
	case 2:
		_, err := f.Inventory.AppendEvents(ctx, []dinv.Event{{PR: ref, Kind: dinv.KindReview, Key: fmt.Sprintf("script-%d", n), Actor: "user-03", At: at, Data: "CHANGES_REQUESTED:x"}})
		return err
	case 3:
		return f.Inventory.RecordEvent(ctx, dinv.Event{PR: ref, Kind: dinv.KindNudged, Key: fmt.Sprintf("script-%d", n), At: at, Data: "@user-03"})
	case 4:
		return inventorytest.Of(f.Inventory).SetOverride(ctx, dinv.Override{PR: ref, Hold: n%2 == 0, Adopted: true})
	case 5:
		r.propose(ref.Unit(), overlay.Pending{Intent: "follow-up", Reason: fmt.Sprintf("script %d", n), Since: at, Deadline: at.Add(time.Minute)})
		return nil
	case 6:
		p, ok, err := inventorytest.Of(f.Inventory).PR(ctx, ref)
		if err != nil || !ok {
			return fmt.Errorf("%s: %v %v", ref, ok, err)
		}
		p.Title = fmt.Sprintf("Scripted %d", n)
		if n%3 == 0 {
			p.State, p.MergedAt, p.ClosedAt = "MERGED", at, at
		}
		return inventorytest.Of(f.Inventory).UpsertPR(ctx, p)
	case 7:
		return inventorytest.Of(f.Inventory).SetReasons(ctx, f.Open[58+(n/9)%4], nil)
	default:
		return inventorytest.Of(f.Inventory).SetReasons(ctx, f.Open[58+(n/9+2)%4], []string{"review-requested"})
	}
}

func TestProjectionMatchesAFreshDerive(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	r := newRig(t, f)
	r.openIntakes(f.Open[1], f.Open[2])
	all := &client{}
	all.pull(r)
	for n := range 50 {
		if err := writer(r, n); err != nil {
			t.Fatalf("write %d: %v", n, err)
		}
		if n%5 == 4 {
			all.pull(r)
		}
		if n%10 == 9 {
			r.advance(time.Minute)
		}
	}
	all.pull(r)
	if all.removed == 0 {
		t.Fatal("no delta removed a row")
	}
	r.matchesFresh(encoded(t, r.rows(r.m, api.Since{}).Rows))
	r.matchesFresh(encoded(t, all.all()))
}

func TestProjectionConvergesUnderConcurrentWrites(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	r := newRig(t, f)
	r.openIntakes(f.Open[1], f.Open[2])
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	published := make(chan api.Change, 1024)
	go func() {
		r.m.Run(ctx, func(c api.Change) { published <- c })
		close(runDone)
	}()
	all := &client{}
	all.pull(r)
	var writers, readers sync.WaitGroup
	for w := range 4 {
		writers.Go(func() {
			for i := range 25 {
				if err := writer(r, w*25+i); err != nil {
					t.Errorf("write %d: %v", w*25+i, err)
					return
				}
			}
		})
	}
	stop := make(chan struct{})
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := r.m.Rows(context.Background(), api.Since{}); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	pulls := 0
	follow := func(c api.Change) {
		if c.Epoch != all.since.Epoch || c.Type != "changed" {
			t.Errorf("change %+v after %+v", c, all.since)
		}
		if c.Rev <= all.since.Rev {
			return
		}
		all.pull(r)
		pulls++
	}
	writersDone := make(chan struct{})
	go func() { writers.Wait(); close(writersDone) }()
	for done := false; !done; {
		select {
		case c := <-published:
			follow(c)
		case <-writersDone:
			done = true
		}
	}
	close(stop)
	readers.Wait()
	cur := r.rows(r.m, api.Since{}).Rev
	for all.since.Rev < cur && !t.Failed() {
		select {
		case c := <-published:
			follow(c)
		case <-time.After(patience):
			t.Fatalf("no change published up to rev %d; the client is at %d", cur, all.since.Rev)
		}
	}
	cancel()
	<-runDone
	all.pull(r)
	if t.Failed() {
		return
	}
	r.matchesFresh(encoded(t, r.rows(r.m, api.Since{}).Rows))
	r.matchesFresh(encoded(t, all.all()))
	if pulls == 0 {
		t.Error("no change was published")
	}
}

func TestMinuteTickChangesNothingAtLiveData(t *testing.T) {
	r := newRig(t, storetest.Seeded(t, storetest.Live))
	before := r.rows(r.m, api.Since{})
	r.advance(time.Minute)
	ctx, sc := metertest.Start(t, context.Background())
	after, err := r.m.Rows(ctx, api.Since{Epoch: before.Epoch, Rev: before.Rev})
	if err != nil {
		t.Fatal(err)
	}
	if metertest.Count(sc, metertest.Limit{Res: meter.Tx, Op: "read"}) != 1 {
		t.Errorf("the minute moved and no rebuild ran")
	}
	if after.Rev != before.Rev || after.Full || len(after.Rows) != 0 || len(after.Removed) != 0 {
		t.Fatalf("a minute changed rows: rev %d to %d, %d rows, removed %v", before.Rev, after.Rev, len(after.Rows), after.Removed)
	}
}

func TestClockFlipsStalledAndRenudge(t *testing.T) {
	f := storetest.Empty(t)
	ctx := context.Background()
	r := newRig(t, f)
	waits := pr.Ref{Owner: "acme", Repo: "api", Number: 1}
	nudged := pr.Ref{Owner: "acme", Repo: "api", Number: 2}
	for _, p := range []dinv.PR{
		{Ref: waits, Title: "Waits", Author: "ana", State: "OPEN", HeadOid: "h1", ReviewRequests: []string{viewer}, Reasons: []string{"review-requested"}},
		{Ref: nudged, Title: "Nudged", Author: viewer, State: "OPEN", HeadOid: "h2", ReviewRequests: []string{"bia"}, Reasons: []string{"author"}},
	} {
		if err := inventorytest.Of(f.Inventory).UpsertPR(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	asked := f.Now
	for workday.Between(asked, f.Now) < time.Duration(r.cfg.GitHub.Stalled.Review) {
		asked = asked.Add(-time.Minute)
	}
	asked = asked.Add(30 * time.Second)
	if _, err := f.Inventory.AppendEvents(ctx, []dinv.Event{
		{PR: waits, Kind: dinv.KindPush, Key: "h1", Actor: "ana", At: asked},
		{PR: waits, Kind: dinv.KindReviewRequest, Key: viewer, Actor: "ana", At: asked},
		{PR: nudged, Kind: dinv.KindPush, Key: "h2", Actor: viewer, At: f.Now.Add(-48 * time.Hour)},
		{PR: nudged, Kind: dinv.KindReviewRequest, Key: "bia", Actor: viewer, At: f.Now.Add(-48 * time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.Inventory.RecordEvent(ctx, dinv.Event{PR: nudged, Kind: dinv.KindNudged, Key: "n1", At: f.Now.Add(-prrow.RenudgeAfter + 30*time.Second), Data: "@bia"}); err != nil {
		t.Fatal(err)
	}
	nudge := func(rows []api.Row) api.ActionState {
		for _, row := range rows {
			if row.Unit == nudged.Unit() {
				i := slices.IndexFunc(row.Actions, func(a api.ActionState) bool { return a.ID == "nudge" })
				if i >= 0 {
					return row.Actions[i]
				}
			}
		}
		t.Fatal("no nudge on the nudged pull request")
		return api.ActionState{}
	}
	stalled := func(rows []api.Row) bool {
		i := slices.IndexFunc(rows, func(row api.Row) bool { return row.Unit == waits.Unit() })
		return i >= 0 && rows[i].Summary.Stalled
	}
	before := r.rows(r.m, api.Since{})
	if stalled(before.Rows) || nudge(before.Rows).Enabled {
		t.Fatalf("before the minute: stalled %v, nudge %+v", stalled(before.Rows), nudge(before.Rows))
	}
	r.advance(time.Minute)
	after := r.rows(r.m, api.Since{Epoch: before.Epoch, Rev: before.Rev})
	if after.Rev == before.Rev || after.Full || len(after.Rows) != 2 {
		t.Fatalf("the minute moved: rev %d to %d, full %v, %d rows", before.Rev, after.Rev, after.Full, len(after.Rows))
	}
	if !stalled(after.Rows) || !nudge(after.Rows).Enabled {
		t.Fatalf("after the minute: stalled %v, nudge %+v", stalled(after.Rows), nudge(after.Rows))
	}
}

func TestCheckCleanOnFixture(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	r := newRig(t, f)
	r.openIntakes(f.Open[1], f.Open[2])
	r.rows(r.m, api.Since{})
	for n := range 20 {
		if err := writer(r, n); err != nil {
			t.Fatalf("write %d: %v", n, err)
		}
	}
	if diffs := r.m.Check(context.Background()); len(diffs) != 0 {
		t.Fatalf("a model that saw every note differs: %q", diffs)
	}
}

func TestCheckFindsAStaleRow(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	r := newRig(t, f)
	r.rows(r.m, api.Since{})
	ref := f.Open[4]
	sqlitetest.Exec(t, f.DB, `UPDATE pull_requests SET title = 'Written behind the log' WHERE owner = ? AND repo = ? AND number = ?`, ref.Owner, ref.Repo, ref.Number)
	diffs := r.m.Check(context.Background())
	if want := []string{ref.Unit().String() + ": differs in title"}; !slices.Equal(diffs, want) {
		t.Fatalf("check %q, want %q", diffs, want)
	}
	if row, _, _ := r.m.Row(context.Background(), ref.Unit(), catalog.Asked{}); row.Title == "Written behind the log" {
		t.Fatal("the check changed the served row")
	}
}

func overrideOf(r api.Row) api.Override {
	p, _ := prrow.PayloadOf(r)
	return p.Override
}

func TestUpcomingUnmuteReadsWakes(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	r := newRig(t, f)
	ctx := context.Background()
	ref := f.Open[3]
	until := f.Now.Add(5 * time.Hour)
	if err := mute(ctx, f, ref, until); err != nil {
		t.Fatal(err)
	}
	row, ok, err := r.m.Row(ctx, ref.Unit(), catalog.Asked{})
	if err != nil || !ok {
		t.Fatalf("row %v %v", ok, err)
	}
	p, _ := prrow.PayloadOf(row)
	if p.Facts == nil || !slices.ContainsFunc(p.Facts.Next, func(u api.Upcoming) bool { return u.What == api.UpcomingUnmute && u.At.Equal(until) }) {
		t.Fatalf("next %+v, want the wake's until", p.Facts)
	}
	if err := f.DB.Wakes().Drop(ctx, task.ID("t-"+strings.ReplaceAll(ref.String(), "/", "-"))); err != nil {
		t.Fatal(err)
	}
	row, _, _ = r.m.Row(ctx, ref.Unit(), catalog.Asked{})
	if p, _ := prrow.PayloadOf(row); p.Facts != nil && slices.ContainsFunc(p.Facts.Next, func(u api.Upcoming) bool { return u.What == api.UpcomingUnmute }) {
		t.Fatalf("a dropped wake is still next: %+v", p.Facts.Next)
	}
}
