package inventorytest

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

const everyTopic = ^port.Topic(0)

// writeCase pins what one write method notes: seed runs first and its
// notes are left out; call must note exactly once.
type writeCase struct {
	method string
	name   string
	seed   func(t *testing.T, s *Store)
	call   func(ctx context.Context, s *Store) error
	topics port.Topic
	refs   []github.Ref
	all    bool
	// host says the call records events on the host after its write: a
	// second note.
	host bool
}

// writes is every exported write method of Store; a method may have several
// rows.
var writes = []writeCase{
	{
		method: "UpsertPR", name: "/new",
		call:   func(ctx context.Context, s *Store) error { return s.UpsertPR(ctx, openPR(ref, "author")) },
		topics: port.TopicPRs | port.TopicFollow | port.TopicSourceEvents, refs: []github.Ref{ref}, host: true,
	},
	{
		method: "UpsertPR", name: "/same reasons",
		seed: seedPRs("author", ref),
		call: func(ctx context.Context, s *Store) error {
			pr := openPR(ref, "author")
			pr.Title = "renamed"
			return s.UpsertPR(ctx, pr)
		},
		topics: port.TopicPRs, refs: []github.Ref{ref},
	},
	{
		method: "SaveSnapshots", name: "/new detail",
		seed: seedPRs("author", ref),
		call: func(ctx context.Context, s *Store) error {
			_, err := s.SaveSnapshots(ctx, []Snapshot{{PR: openPR(ref, "author"), Detail: &inventory.Detail{Version: inventory.DetailVersion, Labels: []string{"bug"}}}}, noteAt)
			return err
		},
		topics: port.TopicPRs, refs: []github.Ref{ref},
	},
	{
		method: "SaveSnapshots", name: "/new with events",
		seed: seedPRs("author", ref),
		call: func(ctx context.Context, s *Store) error {
			pr := openPR(ref2, "author")
			_, err := s.SaveSnapshots(ctx, []Snapshot{
				{PR: openPR(ref, "author")},
				{PR: pr, Events: []dinv.Event{{PR: ref2, Kind: dinv.KindOpened, At: noteAt}}},
			}, noteAt.Add(time.Hour))
			return err
		},
		topics: port.TopicPRs | port.TopicFollow | port.TopicSourceEvents, refs: []github.Ref{ref2}, host: true,
	},
	{
		method: "SaveSnapshots", name: "/a merge filled",
		seed: func(t *testing.T, s *Store) {
			seedPRs("author", ref)(t, s)
			if _, err := s.AppendEvents(context.Background(), []dinv.Event{{PR: ref, Kind: dinv.KindMerged, At: noteAt}}); err != nil {
				t.Fatal(err)
			}
		},
		call: func(ctx context.Context, s *Store) error {
			merged := dinv.Event{PR: ref, Kind: dinv.KindMerged, At: noteAt, Actor: "bob", Data: "abc"}
			_, err := s.SaveSnapshots(ctx, []Snapshot{{PR: openPR(ref, "author"), Events: []dinv.Event{merged}, Merges: []dinv.Event{merged}}}, noteAt)
			return err
		},
		topics: port.TopicSourceEvents, refs: []github.Ref{ref},
	},
	{
		method: "SetReasons",
		seed:   seedPRs("author", ref),
		call:   func(ctx context.Context, s *Store) error { return s.SetReasons(ctx, ref, nil) },
		topics: port.TopicPRs | port.TopicFollow | port.TopicSourceEvents, refs: []github.Ref{ref}, host: true,
	},
	{
		method: "SetWorkspace",
		seed:   seedPRs("", ref),
		call:   func(ctx context.Context, s *Store) error { return s.SetWorkspace(ctx, ref, dinv.Workspace{ID: "w1"}) },
		topics: port.TopicPRs | port.TopicFollow | port.TopicSourceEvents, refs: []github.Ref{ref}, host: true,
	},
	{
		method: "ReplaceWorkspaces",
		seed: func(t *testing.T, s *Store) {
			seedPRs("author", ref, ref2, ref3)(t, s)
			for r, id := range map[github.Ref]string{ref: "w1", ref3: "w3"} {
				if err := s.SetWorkspace(context.Background(), r, dinv.Workspace{ID: id}); err != nil {
					t.Fatal(err)
				}
			}
		},
		call: func(ctx context.Context, s *Store) error {
			return s.ReplaceWorkspaces(ctx, map[github.Ref]dinv.Workspace{ref2: {ID: "w2"}, ref3: {ID: "w3"}})
		},
		topics: port.TopicPRs | port.TopicFollow, refs: []github.Ref{ref, ref2},
	},
	{
		method: "SetViewer", name: "/new login",
		call:   func(ctx context.Context, s *Store) error { return s.SetViewer(ctx, unit.GitHub, "me") },
		topics: port.TopicViewer,
	},
	{
		method: "SetMetaNoting",
		call: func(ctx context.Context, s *Store) error {
			return s.SetMetaNoting(ctx, "scope_resolved", "{}", port.TopicViewer)
		},
		topics: port.TopicMeta | port.TopicViewer,
	},
	{
		method: "AppendEvents",
		call: func(ctx context.Context, s *Store) error {
			_, err := s.AppendEvents(ctx, []dinv.Event{{PR: ref, Kind: dinv.KindReview, Key: "1", At: noteAt}})
			return err
		},
		topics: port.TopicSourceEvents, refs: []github.Ref{ref},
	},
	{
		method: "RecordEvent", name: "/unkeyed",
		call: func(ctx context.Context, s *Store) error {
			return s.RecordEvent(ctx, dinv.Event{PR: ref, Kind: dinv.KindStatus, At: noteAt, Data: "Done"})
		},
		topics: port.TopicLoopEvents, refs: []github.Ref{ref},
	},
	{
		method: "RecordUnitEvent",
		call: func(ctx context.Context, s *Store) error {
			return s.RecordUnitEvent(ctx, port.Event{Unit: ref.Unit(), Kind: dinv.KindRoundUnblocked, At: noteAt, Data: "passed"})
		},
		topics: port.TopicLoopEvents, refs: []github.Ref{ref},
	},
	{
		method: "RecordEvent", name: "/keyed without a pull request",
		call: func(ctx context.Context, s *Store) error {
			return s.RecordEvent(ctx, dinv.Event{Kind: "breaker_opened", Key: "k", At: noteAt})
		},
		topics: port.TopicLoopEvents,
	},
	{
		method: "SetOverride", name: "/track",
		seed: seedPRs("", ref),
		call: func(ctx context.Context, s *Store) error {
			return s.SetOverride(ctx, dinv.Override{PR: ref, Tracked: true})
		},
		topics: port.TopicOverrides | port.TopicPRs | port.TopicFollow | port.TopicSourceEvents, refs: []github.Ref{ref}, host: true,
	},
	{
		method: "SetOverride", name: "/pause",
		call: func(ctx context.Context, s *Store) error {
			return s.SetOverride(ctx, dinv.Override{PR: ref, Hold: true})
		},
		topics: port.TopicOverrides, refs: []github.Ref{ref},
	},
	{
		method: "SetTracked",
		seed:   seedPRs("", ref),
		call: func(ctx context.Context, s *Store) error {
			_, err := s.SetTracked(ctx, ref, true)
			return err
		},
		topics: port.TopicOverrides | port.TopicPRs | port.TopicFollow | port.TopicSourceEvents, refs: []github.Ref{ref}, host: true,
	},
	{
		method: "SetAdopted",
		call:   func(ctx context.Context, s *Store) error { return s.SetAdopted(ctx, ref) },
		topics: port.TopicOverrides, refs: []github.Ref{ref},
	},
	{
		method: "SetNextGuidance",
		call:   func(ctx context.Context, s *Store) error { return s.SetNextGuidance(ctx, ref, "go") },
		topics: port.TopicOverrides, refs: []github.Ref{ref},
	},
	{
		method: "KeepNextGuidance",
		call:   func(ctx context.Context, s *Store) error { return s.KeepNextGuidance(ctx, ref, "go") },
		topics: port.TopicOverrides, refs: []github.Ref{ref},
	},
	{
		method: "TakeNextGuidance",
		seed:   seedGuidance,
		call: func(ctx context.Context, s *Store) error {
			_, err := s.TakeNextGuidance(ctx, ref)
			return err
		},
		topics: port.TopicOverrides, refs: []github.Ref{ref},
	},
	{
		method: "SetMeta",
		call:   func(ctx context.Context, s *Store) error { return s.SetMeta(ctx, "k", "v") },
		topics: port.TopicMeta,
	},
	{
		method: "SaveTurn",
		call: func(ctx context.Context, s *Store) error {
			return s.SaveTurn(ctx, dinv.StoredTurn{ID: "t1", Kind: "review", Repo: "acme/api", Text: "x"}, noteAt)
		},
		topics: port.TopicRounds,
	},
	{
		method: "RecordSituations",
		call: func(ctx context.Context, s *Store) error {
			return s.RecordSituations(ctx, []dinv.Situation{{PR: ref, Data: "review:me", At: noteAt}})
		},
		topics: port.TopicLoopEvents, refs: []github.Ref{ref},
	},
	{
		method: "StartBriefRun",
		call: func(ctx context.Context, s *Store) error {
			_, err := s.StartBriefRun(ctx, fullRun())
			return err
		},
		topics: port.TopicBriefs, refs: []github.Ref{ref},
	},
	{
		method: "EndBriefRun",
		seed:   seedRun,
		call: func(ctx context.Context, s *Store) error {
			return s.EndBriefRun(ctx, endOK(fullRun(), 1), dinv.BriefRecord{})
		},
		topics: port.TopicBriefs | port.TopicLoopEvents, refs: []github.Ref{ref},
	},
	{
		method: "InterruptBriefRuns",
		seed:   seedRun,
		call: func(ctx context.Context, s *Store) error {
			_, err := s.InterruptBriefRuns(ctx, noteAt)
			return err
		},
		topics: port.TopicBriefs | port.TopicLoopEvents, refs: []github.Ref{ref},
	},
	{
		method: "WantBrief",
		call:   func(ctx context.Context, s *Store) error { return s.WantBrief(ctx, ref, noteAt) },
		topics: port.TopicBriefs | port.TopicLoopEvents, refs: []github.Ref{ref},
	},
	{
		method: "ClearWanted",
		seed:   seedWanted,
		call:   func(ctx context.Context, s *Store) error { return s.ClearWanted(ctx, ref) },
		topics: port.TopicBriefs, refs: []github.Ref{ref},
	},
	{
		method: "RefreshBrief",
		seed:   seedBrief,
		call:   func(ctx context.Context, s *Store) error { return s.RefreshBrief(ctx, ref) },
		topics: port.TopicBriefs | port.TopicLoopEvents, refs: []github.Ref{ref},
	},
	{
		method: "SetMetas",
		call: func(ctx context.Context, s *Store) error {
			return s.SetMetas(ctx, map[string]string{"a": "1", "b": "2"})
		},
		topics: port.TopicMeta,
	},
}

var (
	ref2   = github.Ref{Owner: "acme", Repo: "api", Number: 2}
	ref3   = github.Ref{Owner: "acme", Repo: "web", Number: 3}
	noteAt = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
)

func openPR(r github.Ref, reason string) dinv.PR {
	pr := dinv.PR{Ref: r, State: dgh.StateOpen, LastSeenAt: noteAt}
	if reason != "" {
		pr.Reasons = []string{reason}
	}
	return pr
}

func seedPRs(reason string, refs ...github.Ref) func(t *testing.T, s *Store) {
	return func(t *testing.T, s *Store) {
		for _, r := range refs {
			upsert(t, s, openPR(r, reason))
		}
	}
}

var sameLinks = map[github.Ref]dinv.Workspace{
	ref:  {ID: "w1", DisplayName: "api", Status: "In review", AgentStates: []turn.AgentState{turn.ParseAgentState("working")}},
	ref2: {ID: "w2", IsMain: true},
}

func seedRun(t *testing.T, s *Store) {
	if id, err := s.StartBriefRun(context.Background(), fullRun()); err != nil || id != 1 {
		t.Fatalf("run %d: %v", id, err)
	}
}

func seedBrief(t *testing.T, s *Store) {
	seedRun(t, s)
	if err := s.EndBriefRun(context.Background(), endOK(fullRun(), 1), dinv.BriefRecord{}); err != nil {
		t.Fatal(err)
	}
}

func seedWanted(t *testing.T, s *Store) {
	if err := s.WantBrief(context.Background(), ref, noteAt); err != nil {
		t.Fatal(err)
	}
}

func seedGuidance(t *testing.T, s *Store) {
	if err := s.SetNextGuidance(context.Background(), ref, "go"); err != nil {
		t.Fatal(err)
	}
}

// reads are the exported methods of Store that write nothing.
var reads = []string{
	"UseClock",
	"PR", "PRs", "PRsWithOverrides", "Seen", "LastEvent", "ListPRs", "Events", "EventsByPR", "UnitlessEvents", "FirstRoundBlocks",
	"RecentActivity", "BriefStates", "Tracked", "Override", "Overrides", "UnitMutes", "Meta", "MetaPrefixed", "Viewer", "Viewers", "Now", "Turn", "LastBundledSHA",
	"Timings", "Tags", "TagVocabulary", "BriefRuns", "BriefRun", "BriefRecord", "BriefRecords", "UnitBrief", "Ended",
	"Details", "Host", "List", "PRsByUnit",
}

// silent are the exported write methods that note nothing.
var silent = []string{"UseHost", "DB", "UsePRs"}

func openNoted(t *testing.T) (*Store, *change.Log) {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "notes.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return Of(inventory.New(db)), log
}

// noted runs fn and returns the merge of what it noted and how many notes.
func noted(t *testing.T, log *change.Log, fn func() error) (port.Change, int) {
	t.Helper()
	from := log.Gen()
	if err := fn(); err != nil {
		t.Fatal(err)
	}
	return log.Since(from, everyTopic), int(log.Gen() - from)
}

func unitsOf(refs []github.Ref) []unit.Unit {
	var out []unit.Unit
	for _, r := range refs {
		out = append(out, r.Unit())
	}
	slices.SortFunc(out, func(a, b unit.Unit) int {
		if a.Less(b) {
			return -1
		}
		if b.Less(a) {
			return 1
		}
		return 0
	})
	return out
}

func TestEveryWriteNotes(t *testing.T) {
	for _, c := range writes {
		t.Run(c.method+c.name, func(t *testing.T) {
			s, log := openNoted(t)
			ctx := context.Background()
			if c.seed != nil {
				c.seed(t, s)
			}
			got, n := noted(t, log, func() error { return c.call(ctx, s) })
			want := 1
			if c.host {
				want = 2
			}
			if n != want {
				t.Fatalf("%d notes, want %d: %+v", n, want, got)
			}
			if got.Topics != c.topics || got.All != c.all {
				t.Errorf("noted topics %b all %v, want %b all %v", got.Topics, got.All, c.topics, c.all)
			}
			if want := unitsOf(c.refs); !slices.Equal(got.Units, want) {
				t.Errorf("noted units %v, want %v", got.Units, want)
			}
		})
	}
}

func TestEveryExportedMethodIsReadOrNoted(t *testing.T) {
	known := map[string]bool{}
	for _, m := range append(slices.Clone(reads), silent...) {
		known[m] = true
	}
	for _, c := range writes {
		known[c.method] = true
	}
	typ := reflect.TypeFor[*Store]()
	for i := range typ.NumMethod() {
		if m := typ.Method(i).Name; !known[m] {
			t.Errorf("Store.%s is neither in reads nor in writes", m)
		}
	}
	for m := range known {
		if _, ok := typ.MethodByName(m); !ok {
			t.Errorf("%s is listed but Store has no such method", m)
		}
	}
}

// TestNoOpWriteNotesNothing: a write method that changes no row notes nothing.
func TestNoOpWriteNotesNothing(t *testing.T) {
	cases := []struct {
		name string
		seed func(t *testing.T, s *Store)
		call func(ctx context.Context, s *Store) error
	}{
		{"SaveSnapshots/same snapshots", seedPRs("author", ref, ref2), func(ctx context.Context, s *Store) error {
			_, err := s.SaveSnapshots(ctx, []Snapshot{{PR: openPR(ref, "author")}, {PR: openPR(ref2, "author")}}, noteAt.Add(time.Hour))
			return err
		}},
		{"UpsertPR", seedPRs("author", ref), func(ctx context.Context, s *Store) error { return s.UpsertPR(ctx, openPR(ref, "author")) }},
		{"SetReasons", seedPRs("author", ref), func(ctx context.Context, s *Store) error {
			return s.SetReasons(ctx, ref, []string{"author"})
		}},
		{"SetWorkspace", seedPRs("", ref), func(ctx context.Context, s *Store) error {
			return s.SetWorkspace(ctx, ref, dinv.Workspace{})
		}},
		{"ReplaceWorkspaces", seedPRs("author", ref), func(ctx context.Context, s *Store) error {
			return s.ReplaceWorkspaces(ctx, nil)
		}},
		{"AppendEvents", func(t *testing.T, s *Store) {
			if _, err := s.AppendEvents(context.Background(), []dinv.Event{{PR: ref, Kind: dinv.KindReview, Key: "1", At: noteAt}}); err != nil {
				t.Fatal(err)
			}
		}, func(ctx context.Context, s *Store) error {
			_, err := s.AppendEvents(ctx, []dinv.Event{{PR: ref, Kind: dinv.KindReview, Key: "1", At: noteAt}})
			return err
		}},
		{"SetOverride", func(t *testing.T, s *Store) {
			if err := s.SetOverride(context.Background(), dinv.Override{PR: ref, Hold: true}); err != nil {
				t.Fatal(err)
			}
		}, func(ctx context.Context, s *Store) error {
			return s.SetOverride(ctx, dinv.Override{PR: ref, Hold: true})
		}},
		{"SetTracked", nil, func(ctx context.Context, s *Store) error {
			_, err := s.SetTracked(ctx, ref, false)
			return err
		}},
		{"SetAdopted", func(t *testing.T, s *Store) {
			if err := s.SetAdopted(context.Background(), ref); err != nil {
				t.Fatal(err)
			}
		}, func(ctx context.Context, s *Store) error { return s.SetAdopted(ctx, ref) }},
		{"SetNextGuidance", seedGuidance, func(ctx context.Context, s *Store) error { return s.SetNextGuidance(ctx, ref, "go") }},
		{"KeepNextGuidance", seedGuidance, func(ctx context.Context, s *Store) error { return s.KeepNextGuidance(ctx, ref, "other") }},
		{"TakeNextGuidance", nil, func(ctx context.Context, s *Store) error {
			_, err := s.TakeNextGuidance(ctx, ref)
			return err
		}},
		{"RecordSituations", func(t *testing.T, s *Store) {
			if err := s.RecordSituations(context.Background(), []dinv.Situation{{PR: ref, Data: "review:me", At: noteAt}}); err != nil {
				t.Fatal(err)
			}
		}, func(ctx context.Context, s *Store) error {
			return s.RecordSituations(ctx, []dinv.Situation{{PR: ref, Data: "review:me", At: noteAt.Add(time.Hour)}})
		}},
		{"InterruptBriefRuns", nil, func(ctx context.Context, s *Store) error {
			_, err := s.InterruptBriefRuns(ctx, noteAt)
			return err
		}},
		{"WantBrief", seedWanted, func(ctx context.Context, s *Store) error { return s.WantBrief(ctx, ref, noteAt.Add(time.Hour)) }},
		{"ClearWanted", nil, func(ctx context.Context, s *Store) error { return s.ClearWanted(ctx, ref) }},
		{"RefreshBrief", nil, func(ctx context.Context, s *Store) error { return s.RefreshBrief(ctx, ref) }},
		{"SetMeta/unchanged", func(t *testing.T, s *Store) {
			if err := s.SetMeta(context.Background(), "k", "v"); err != nil {
				t.Fatal(err)
			}
		}, func(ctx context.Context, s *Store) error { return s.SetMeta(ctx, "k", "v") }},
		{"ReplaceWorkspaces/same links", func(t *testing.T, s *Store) {
			seedPRs("author", ref, ref2)(t, s)
			if err := s.ReplaceWorkspaces(context.Background(), sameLinks); err != nil {
				t.Fatal(err)
			}
		}, func(ctx context.Context, s *Store) error { return s.ReplaceWorkspaces(ctx, sameLinks) }},
		{"ClearWanted/cleared", func(t *testing.T, s *Store) {
			seedWanted(t, s)
			if err := s.ClearWanted(context.Background(), ref); err != nil {
				t.Fatal(err)
			}
		}, func(ctx context.Context, s *Store) error { return s.ClearWanted(ctx, ref) }},
		{"RefreshBrief/refreshed", func(t *testing.T, s *Store) {
			seedBrief(t, s)
			if err := s.RefreshBrief(context.Background(), ref); err != nil {
				t.Fatal(err)
			}
		}, func(ctx context.Context, s *Store) error { return s.RefreshBrief(ctx, ref) }},
		{"TakeNextGuidance/empty guidance", func(t *testing.T, s *Store) {
			if err := s.SetOverride(context.Background(), dinv.Override{PR: ref, Hold: true}); err != nil {
				t.Fatal(err)
			}
		}, func(ctx context.Context, s *Store) error {
			_, err := s.TakeNextGuidance(ctx, ref)
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, log := openNoted(t)
			if c.seed != nil {
				c.seed(t, s)
			}
			if got, n := noted(t, log, func() error { return c.call(context.Background(), s) }); n != 0 {
				t.Fatalf("%d notes: %+v", n, got)
			}
		})
	}
}

func countRowWrites(t *testing.T, s *Store) func() int {
	t.Helper()
	sqlitetest.Exec(t, s.db, `CREATE TABLE row_writes (n INTEGER)`)
	sqlitetest.Exec(t, s.db, `CREATE TRIGGER count_row_writes AFTER UPDATE ON pull_requests BEGIN INSERT INTO row_writes VALUES (1); END`)
	return func() int {
		var n int
		if err := s.db.R().QueryRowContext(context.Background(), `SELECT count(*) FROM row_writes`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
}

func TestUpsertSameSnapshotWritesNothing(t *testing.T) {
	s, log := openNoted(t)
	pr := openPR(ref, "author")
	pr.Title = "same"
	pr.ReviewRequests = []string{"bob"}
	upsert(t, s, pr)
	writes := countRowWrites(t, s)
	ctx, sc := metertest.Start(t, context.Background())
	got, n := noted(t, log, func() error { return s.UpsertPR(ctx, pr) })
	if n != 0 {
		t.Fatalf("%d notes: %+v", n, got)
	}
	if w := writes(); w != 0 {
		t.Fatalf("%d row updates, want 0", w)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL, Op: "exec"}: 2})

	pr.LastSeenAt = pr.LastSeenAt.Add(time.Hour)
	if _, n := noted(t, log, func() error { return s.UpsertPR(ctx, pr) }); n != 0 {
		t.Fatalf("a later sighting noted %d times", n)
	}
	if w := writes(); w != 0 {
		t.Fatalf("%d row updates after a later sighting, want 0", w)
	}
	if stored, _, err := s.PR(ctx, ref); err != nil || !stored.LastSeenAt.Equal(pr.LastSeenAt) {
		t.Fatalf("last seen %v, %v", stored.LastSeenAt, err)
	}
}

func TestSetViewerNotesOnlyAChangeAndDropsTheOldRow(t *testing.T) {
	s, log := openNoted(t)
	ctx := context.Background()
	if err := s.SetMeta(ctx, "viewer", "stale"); err != nil {
		t.Fatal(err)
	}
	_, n := noted(t, log, func() error { return s.SetViewer(ctx, unit.GitHub, "me") })
	if n != 1 {
		t.Fatalf("%d notes for a new login", n)
	}
	if got, _ := s.Meta(ctx, "viewer"); got != "" {
		t.Fatalf("v5 viewer row kept: %q", got)
	}
	_, n = noted(t, log, func() error { return s.SetViewer(ctx, unit.GitHub, "me") })
	if n != 0 {
		t.Fatalf("%d notes for the same login", n)
	}
	got, err := s.Viewers(ctx)
	if err != nil || got[unit.GitHub] != "me" || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestSetMetasNotesOnlyAChange(t *testing.T) {
	s, log := openNoted(t)
	ctx := context.Background()
	kv := map[string]string{"a": "1", "b": "2"}
	if err := s.SetMetas(ctx, kv); err != nil {
		t.Fatal(err)
	}
	if _, n := noted(t, log, func() error { return s.SetMetas(ctx, kv) }); n != 0 {
		t.Fatalf("unchanged values: %d notes", n)
	}
	if _, n := noted(t, log, func() error { return s.SetMetas(ctx, map[string]string{"a": "1", "b": "3"}) }); n != 1 {
		t.Fatalf("one changed value: %d notes", n)
	}
	if v, _ := s.Meta(ctx, "b"); v != "3" {
		t.Fatalf("b = %q", v)
	}
}
