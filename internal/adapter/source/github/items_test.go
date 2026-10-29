package ghsource

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/source/github/ghsourcetest"
	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/config"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/round"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"
	"github.com/wloophq/wloop/internal/situation"
	"github.com/wloophq/wloop/internal/turn"
)

type goldenRow struct {
	Name        string       `json:"name"`
	Kind        api.Kind     `json:"kind"`
	UnmutedKind string       `json:"unmuted_kind,omitempty"`
	WaitsOn     string       `json:"waits_on"`
	NeedsMe     bool         `json:"needs_me"`
	InScope     bool         `json:"in_scope"`
	Items       []items.Item `json:"items"`
}

// pinLocal sets time.Local for one test: held ids name the local day.
func pinLocal(t *testing.T, loc *time.Location) {
	t.Helper()
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
}

func brt(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skip(err)
	}
	return loc
}

func TestItemsPerSituation(t *testing.T) {
	pinLocal(t, time.UTC)
	var got []goldenRow
	for _, c := range ghsourcetest.Cases() {
		p, _ := prrow.PayloadOf(c.Row)
		got = append(got, goldenRow{Name: c.Name, Kind: c.Row.Kind, UnmutedKind: p.UnmutedKind, WaitsOn: c.Row.Summary.WaitsOn,
			NeedsMe: prrow.NeedsMe(c.Row), InScope: prrow.InScope(c.Row), Items: brandless(itemsOf(c.Row))})
	}
	raw, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	path := filepath.Join("testdata", "items.golden.json")
	if *update {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, want) {
		t.Errorf("items differ from %s; rerun with -update and review the diff", path)
	}
}

// brandless names the loop's requester by placeholder, so the golden holds under any brand.
func brandless(v []items.Item) []items.Item {
	out := []items.Item{}
	for _, it := range v {
		if it.Requester.Key == "system:"+brand.Name {
			it.Requester = items.Person{Key: "system:<brand>", Name: "<brand>"}
		}
		out = append(out, it)
	}
	return out
}

func TestItemsDeterministic(t *testing.T) {
	for _, r := range ghsourcetest.Rows() {
		a, _ := json.Marshal(itemsOf(r))
		b, _ := json.Marshal(itemsOf(r))
		if !bytes.Equal(a, b) {
			t.Fatalf("%s: %s then %s", r.Label, a, b)
		}
	}
}

func reviewOfViewer(n int, muted bool) api.Row {
	s := ghsourcetest.Situation{PR: ghsourcetest.Open(n, ghsourcetest.Other)}
	s.PR.ReviewRequests = []string{ghsourcetest.Viewer}
	if muted {
		s.Mute = overlay.Mute{UntilStateChange: true}
	}
	return ghsourcetest.Derive(s)
}

func TestMutedRowKeepsItems(t *testing.T) {
	t.Run("pending round", func(t *testing.T) {
		round := func(muted bool) api.Row {
			s := ghsourcetest.Situation{PR: ghsourcetest.Open(3, ghsourcetest.Other)}
			s.PR.ReviewRequests, s.PR.Workspace = []string{ghsourcetest.Viewer}, ghsourcetest.ReviewWorkspace
			s.Pending = &round.Pending{Intent: "review", Reason: "new push", Since: ghsourcetest.Now.Add(-time.Minute), Deadline: ghsourcetest.Now.Add(4 * time.Minute)}
			if muted {
				s.Mute = overlay.Mute{Until: ghsourcetest.Now.Add(time.Hour)}
			}
			return ghsourcetest.Derive(s)
		}
		plain, muted := round(false), round(true)
		if situation.Kind(muted.Kind) != situation.KindMuted {
			t.Fatalf("kind %s", muted.Kind)
		}
		a, _ := json.Marshal(itemsOf(plain))
		b, _ := json.Marshal(itemsOf(muted))
		if !bytes.Equal(a, b) || len(itemsOf(plain)) != 1 || itemsOf(plain)[0].ID != "gh:round" {
			t.Fatalf("plain %s\nmuted %s", a, b)
		}
	})
	plain, muted := reviewOfViewer(1, false), reviewOfViewer(1, true)
	if situation.Kind(muted.Kind) != situation.KindMuted {
		t.Fatalf("kind %s", muted.Kind)
	}
	a, _ := json.Marshal(itemsOf(plain))
	b, _ := json.Marshal(itemsOf(muted))
	if !bytes.Equal(a, b) || len(itemsOf(plain)) != 1 || itemsOf(plain)[0].ID != "gh:review" {
		t.Fatalf("plain %s\nmuted %s", a, b)
	}
}

func TestHeldIDIsTheLocalDay(t *testing.T) {
	loc := brt(t)
	pinLocal(t, loc)
	hold := time.Date(2026, 10, 21, 20, 0, 0, 0, loc)
	held := func(n int, at time.Time, mutedAt time.Time) api.Row {
		s := ghsourcetest.Situation{PR: ghsourcetest.Open(n, ghsourcetest.Other), At: at}
		s.PR.Workspace = ghsourcetest.ReviewWorkspace
		s.Events = []dinv.Event{ghsourcetest.Event(n, dinv.KindRoundHeld, "", hold, "cap reached")}
		if !mutedAt.IsZero() {
			s.Mute = overlay.Mute{Until: at.Add(5 * time.Hour)}
			s.Events = append(s.Events, ghsourcetest.Event(n, dinv.KindMuted, ghsourcetest.Viewer, mutedAt, ""))
		}
		return ghsourcetest.Derive(s)
	}
	now := time.Date(2026, 10, 21, 22, 0, 0, 0, loc)
	rows := []api.Row{held(1, now, time.Time{}), held(1, now, time.Date(2026, 10, 21, 21, 30, 0, 0, loc))}
	for _, r := range rows {
		got := itemsOf(r)
		if len(got) != 1 || got[0].ID != "gh:held:2026-10-21" {
			t.Errorf("%s (%s): %+v", r.Label, r.Kind, got)
		}
	}
}

func TestAskedTurnDerivesNoItem(t *testing.T) {
	for _, c := range ghsourcetest.Cases() {
		switch kindOf(c.Row) {
		case situation.KindLoopWaiting, situation.KindLoopStopped:
			if !loopItemsAsExpected(c.Row) {
				t.Errorf("%s: %+v", c.Name, itemsOf(c.Row))
			}
		}
	}
}

// loopItemsAsExpected: a loop row derives no item, but for an agent that waits on the viewer with no
// question, which derives gh:wait.
func loopItemsAsExpected(r api.Row) bool {
	got := itemsOf(r)
	if situation.Kind(r.Kind) == situation.KindLoopWaiting && r.Question == nil && prrow.NeedsMe(r) {
		return len(got) == 1 && got[0].ID == "gh:wait"
	}
	return len(got) == 0
}

func kindOf(r api.Row) situation.Kind {
	k := situation.Kind(r.Kind)
	if p, ok := prrow.PayloadOf(r); ok && k == situation.KindMuted {
		return situation.Kind(p.UnmutedKind)
	}
	return k
}

func TestTransitionsResolveItems(t *testing.T) {
	ctx := context.Background()
	f := storetest.Empty(t)
	l := &coreitems.Ledger{Store: f.DB.Items(), Clock: fixedClock(ghsourcetest.Now)}
	rev := 0
	derive := func(r api.Row) {
		t.Helper()
		rev++
		c, err := cardOf(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.Reconcile(ctx, r.Unit, itemsOf(r), unit.Anchor{Unit: r.Unit, Ref: "row:" + strings.Repeat("1", rev)}, c.Outcome); err != nil {
			t.Fatal(err)
		}
	}
	review := func(t *testing.T, u unit.Unit) items.Item {
		t.Helper()
		got, err := l.Items(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(got, func(it items.Item) bool { return it.ID == "gh:review" })
		if i < 0 {
			t.Fatalf("no gh:review in %+v", got)
		}
		return got[i]
	}
	cases := []struct {
		name string
		next func(s *ghsourcetest.Situation)
		want items.Resolution
	}{
		{"merged", func(s *ghsourcetest.Situation) {
			s.PR.State, s.PR.MergedAt = "MERGED", ghsourcetest.Now
			s.Events = append(s.Events, ghsourcetest.Event(s.PR.Ref.Number, dinv.KindMerged, ghsourcetest.Other, ghsourcetest.Now, ""))
		}, items.Done},
		{"closed", func(s *ghsourcetest.Situation) { s.PR.State, s.PR.ClosedAt = "CLOSED", ghsourcetest.Now }, items.Moot},
		{"loop working", func(s *ghsourcetest.Situation) {
			s.PR.Workspace = ghsourcetest.ReviewWorkspace
			s.Turn = &prrow.TurnFact{Facts: overlay.Turn{Intent: "review", Role: "pr.review", State: "started", At: ghsourcetest.Now}, Session: "s1"}
		}, items.Superseded},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := ghsourcetest.Situation{PR: ghsourcetest.Open(100+i, ghsourcetest.Other)}
			s.PR.ReviewRequests = []string{ghsourcetest.Viewer}
			derive(ghsourcetest.Derive(s))
			if it := review(t, s.PR.Ref.Unit()); it.State != items.Open {
				t.Fatalf("before: %+v", it)
			}
			tc.next(&s)
			next := ghsourcetest.Derive(s)
			derive(next)
			it := review(t, s.PR.Ref.Unit())
			if it.State != items.Resolved || it.Resolution != tc.want || it.Evidence.Kind != items.EvidenceUnitEvent {
				t.Errorf("after %s: %s %s %s", next.Kind, it.State, it.Resolution, it.Evidence.Kind)
			}
		})
	}
}

type fixedClock time.Time

func (c fixedClock) Now() time.Time { return time.Time(c) }

func hasViewerItem(r api.Row, itemsOf func(api.Row) []items.Item) bool {
	p, _ := prrow.PayloadOf(r)
	me := items.Identity{Keys: []string{"github:" + p.Viewer}}
	return slices.ContainsFunc(itemsOf(r), func(it items.Item) bool {
		return it.State == items.Open && slices.ContainsFunc(it.WaitingOn, func(w items.Person) bool { return w.Is(me) })
	})
}

// ownNext tells whether r is the viewer's own in-scope pull request that
// waits on its author rather than on the viewer: merge, no reviewer, a
// reviewer's comment to answer.
func ownNext(r api.Row) bool {
	p, ok := prrow.PayloadOf(r)
	k := situation.Kind(r.Kind)
	return ok && p.Viewer != "" && strings.EqualFold(p.Author, p.Viewer) && prrow.InScope(r) &&
		(k == situation.KindMerge || k == situation.KindNoReviewer || k == situation.KindReply)
}

func compareNeedsMe(t *testing.T, rows []api.Row, itemsOf func(api.Row) []items.Item) (compared int) {
	t.Helper()
	for _, r := range rows {
		switch kindOf(r) {
		case situation.KindLoopWaiting, situation.KindLoopStopped:
			if !loopItemsAsExpected(r) {
				t.Errorf("%s asked: %+v", r.Label, itemsOf(r))
			}
			continue
		}
		if situation.Kind(r.Kind) == situation.KindMuted {
			continue
		}
		compared++
		want := slices.Contains(r.Views, api.ViewNeedsMe) || ownNext(r)
		if got := hasViewerItem(r, itemsOf); want != got {
			t.Errorf("%s (%s, waits on %s): needs-me or own next %v, viewer item %v", r.Label, r.Kind, r.Summary.WaitsOn, want, got)
		}
	}
	return compared
}

func TestNeedsMeDifferential(t *testing.T) {
	if n := compareNeedsMe(t, ghsourcetest.Rows(), itemsOf); n == 0 {
		t.Fatal("no table row compared")
	}
	f := storetest.Seeded(t, storetest.Live)
	if err := f.Inventory.SetViewer(context.Background(), unit.GitHub, seededViewer); err != nil {
		t.Fatal(err)
	}
	m := seededModel(t, f)
	page, err := m.Rows(context.Background(), api.ViewAll, api.Since{})
	if err != nil {
		t.Fatal(err)
	}
	needs := 0
	for _, r := range page.Rows {
		if slices.Contains(r.Views, api.ViewNeedsMe) {
			needs++
		}
	}
	cards := Cards{Reports: ghreporttest.Inventory{Store: f.Inventory, Now: f.Inventory.Now}}
	n := compareNeedsMe(t, page.Rows, func(r api.Row) []items.Item {
		its, err := cards.Items(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		return its
	})
	if n < storetest.Live.PRs/2 || needs == 0 {
		t.Fatalf("compared %d of %d seeded rows, %d in needs-me", n, len(page.Rows), needs)
	}
	t.Logf("compared %d of %d seeded rows, %d in needs-me", n, len(page.Rows), needs)
}

const seededViewer = "user-01"

func seededModel(t *testing.T, f *storetest.Fixture) *readmodel.Model {
	t.Helper()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	src := prrow.Source{Store: f.Inventory, Reports: ghreporttest.Inventory{Store: f.Inventory, Now: f.Inventory.Now},
		Mutes: readmodel.WakeMutes(f.DB.Wakes().UnitWakes), RowDeps: readmodel.RowDeps{Books: f.Turns, Sessions: f.Turns, Intakes: f.DB.Intakes().Open,
			Settings: func() readmodel.RowSettings {
				return readmodel.RowSettings{Cfg: config.Defaults(), Loaded: l, GateOn: true}
			}}}
	m := readmodel.New(f.Log, f.DB, noPending{}, nil, baseSettings{Base: readmodel.Base{Viewers: map[unit.ProviderID]string{unit.GitHub: seededViewer},
		Specs: catalog.Specs(l)}}, prrow.New(src))
	m.UseClock(func() time.Time { return f.Now })
	return m
}

type noPending struct{}

func (noPending) Pending(context.Context) (map[unit.Unit]round.Pending, error) { return nil, nil }

type baseSettings struct{ Base readmodel.Base }

func (s baseSettings) Settings(context.Context) (readmodel.Base, error) { return s.Base, nil }

func TestStalledRowHasWaitingItem(t *testing.T) {
	want := map[string]string{
		"stalled_review_of_other/mine/in":     "gh:review",
		"stalled_changes_by_viewer/mine/in":   "gh:address",
		"stalled_changes_by_viewer/theirs/in": "gh:fix",
	}
	for _, c := range ghsourcetest.Cases() {
		id, ok := want[c.Name]
		if !ok {
			continue
		}
		delete(want, c.Name)
		if !c.Row.Summary.Stalled || !prrow.InScope(c.Row) {
			t.Fatalf("%s: stalled %v, in scope %v", c.Name, c.Row.Summary.Stalled, prrow.InScope(c.Row))
		}
		if !slices.ContainsFunc(itemsOf(c.Row), func(it items.Item) bool { return it.ID == id && it.State == items.Open && len(it.WaitingOn) > 0 }) {
			t.Errorf("%s (%s, waits on %s): no waiting %s in %+v", c.Name, c.Row.Kind, c.Row.Summary.WaitsOn, id, itemsOf(c.Row))
		}
	}
	if len(want) > 0 {
		t.Fatalf("cases missing: %v", want)
	}
}

func BenchmarkItemsAndCard(b *testing.B) {
	rows := ghsourcetest.Rows()
	ctx := context.Background()
	for b.Loop() {
		for _, r := range rows {
			_ = itemsOf(r)
			if _, err := cardOf(ctx, r); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(rows)), "ns/row")
}

func TestMutedRowKeepsItsFix(t *testing.T) {
	byName := map[string]api.Row{}
	for _, c := range ghsourcetest.Cases() {
		byName[c.Name] = c.Row
	}
	fixes := 0
	for name, muted := range byName {
		plainName, ok := strings.CutSuffix(name, "/muted")
		if !ok {
			continue
		}
		hasFix := func(r api.Row) bool {
			return slices.ContainsFunc(itemsOf(r), func(it items.Item) bool { return it.ID == "gh:fix" })
		}
		plain := byName[plainName]
		if hasFix(plain) {
			fixes++
		}
		if hasFix(plain) != hasFix(muted) {
			t.Errorf("%s: gh:fix %v unmuted, %v muted", plainName, hasFix(plain), hasFix(muted))
		}
	}
	if fixes == 0 {
		t.Fatal("no case derives gh:fix")
	}
}

// waitingAgent is a pull request whose review workspace's agent waits, with question q.
func waitingAgent(q *api.QuestionView) api.Row {
	s := ghsourcetest.Situation{PR: ghsourcetest.Open(5, ghsourcetest.Other)}
	s.PR.Workspace = ghsourcetest.ReviewWorkspace
	s.PR.Workspace.AgentStates = []dturn.AgentState{dturn.AgentWaiting}
	r := ghsourcetest.Derive(s)
	r.Question = q
	return r
}

func TestAWaitingAgentWithNoQuestionIsAnItem(t *testing.T) {
	waiting := waitingAgent
	r := waiting(nil)
	if situation.Kind(r.Kind) != situation.KindLoopWaiting || !prrow.NeedsMe(r) {
		t.Fatalf("kind %s, needs me %v", r.Kind, prrow.NeedsMe(r))
	}
	got := itemsOf(r)
	if len(got) != 1 || got[0].ID != "gh:wait" || got[0].Initial != items.InitialInbox || len(got[0].WaitingOn) != 1 {
		t.Fatalf("%+v", got)
	}
	if got := itemsOf(waiting(&api.QuestionView{State: "asked", RecordID: 3})); len(got) != 0 {
		t.Fatalf("a question derives %+v", got)
	}
}

func TestEveryNoticeHasItsItem(t *testing.T) {
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	d := prrow.Deriver{Now: ghsourcetest.Now, Loaded: l}
	seen := map[prrow.NoticeCase]bool{}
	for _, c := range append(ghsourcetest.Cases(), ghsourcetest.Case{Name: "waiting agent", Row: waitingAgent(nil)}) {
		if !slices.Contains(c.Row.Views, api.ViewNeedsMe) {
			continue
		}
		n, ok := d.Notice(c.Row, nil)
		if !ok {
			continue
		}
		seen[n.Case] = true
		got := itemsOf(c.Row)
		if n.Case == prrow.NoticeQuestion {
			if len(got) != 0 {
				t.Errorf("%s: a question derives %+v", c.Name, got)
			}
			continue
		}
		if len(got) != 1 || !strings.HasPrefix(got[0].ID, n.Case.Item()) || got[0].Initial != items.InitialInbox {
			t.Errorf("%s: notice %s, items %+v", c.Name, n.Case, got)
		}
	}
	for _, c := range []prrow.NoticeCase{prrow.NoticeHeld, prrow.NoticeBlocked, prrow.NoticeWait, prrow.NoticeQuestion} {
		if !seen[c] {
			t.Errorf("no case notifies %s", c)
		}
	}
}

// TestAPermissionPromptIsTheSameWaitItemAsTheWorkspacesAgent holds the wait
// item the host derives when a turn's waiting session sets the row's
// situation to the one the plugin reports when the review workspace's agent
// waits: the same pull request asks the same answer of the viewer.
func TestAPermissionPromptIsTheSameWaitItemAsTheWorkspacesAgent(t *testing.T) {
	prompt := ghsourcetest.Situation{PR: ghsourcetest.Open(5, ghsourcetest.Other)}
	prompt.PR.Workspace = ghsourcetest.ReviewWorkspace
	prompt.Turn = &prrow.TurnFact{Facts: overlay.Turn{Intent: "review", Role: "pr.review", State: "started", At: ghsourcetest.Now}, Session: "s1", Waiting: true}
	host, rep := ghsourcetest.DeriveReport(prompt)
	if situation.Kind(host.Kind) != situation.KindLoopWaiting || rep.Situation.Kind == string(situation.KindLoopWaiting) {
		t.Fatalf("the prompt's row %s over the report's %s", host.Kind, rep.Situation.Kind)
	}
	got := ItemsOf(host, rep)
	want := itemsOf(waitingAgent(nil))
	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("prompt %+v, agent %+v", got, want)
	}
	got[0].Origin.Ref, want[0].Origin.Ref = "", ""
	if a, b := mustMarshal(t, got[0]), mustMarshal(t, want[0]); !bytes.Equal(a, b) {
		t.Fatalf("prompt %s\nagent  %s", a, b)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
