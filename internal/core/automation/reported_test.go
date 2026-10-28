package automation_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/plugins/codec"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// stalledFor is a sweep workflow over sessions that closes one once its fact
// "session.stalled" holds; its predicate fails the test on a fact the engine
// left unresolved.
func stalledFor(t *testing.T) workflow.Workflow {
	return sweepWF("chase", automationtest.MatchFunc(func(v workflow.Vars) bool {
		for name, f := range v.Facts {
			if _, ok := f.(workflow.Temporal); ok {
				t.Errorf("the predicate read %s unresolved", name)
			}
		}
		return v.Facts["session.stalled"] == true && v.Facts["session.waited"].(time.Duration) > 0
	}, "session.stalled", "session.waited"), "session.close")
}

func TestATemporalFactAgesWithTheClock(t *testing.T) {
	r := newRig(t, stalledFor(t))
	r.put(s1, "a", "session.stalled", workflow.Temporal{Kind: workflow.TemporalAfter, Time: t0.Add(-time.Hour), Threshold: 2 * time.Hour},
		"session.waited", workflow.Temporal{Kind: workflow.TemporalSince, Time: t0.Add(-time.Hour)})
	r.put(s2, "b", "session.stalled", workflow.Temporal{Kind: workflow.TemporalAfter, Threshold: time.Nanosecond},
		"session.waited", workflow.Temporal{Kind: workflow.TemporalSince})
	r.sweep()
	if got := r.ran("session.close"); len(got) != 0 {
		t.Fatalf("ran %v before the hour passed", got)
	}
	r.clock.Advance(time.Hour)
	r.sweep()
	if got := r.ran("session.close"); len(got) != 0 {
		t.Fatalf("ran %v at exactly the threshold", got)
	}
	r.clock.Advance(time.Nanosecond)
	r.sweep()
	if got := r.ran("session.close"); len(got) != 1 || got[0] != s1 {
		t.Fatalf("ran %v once the threshold passed; the zero time never holds", got)
	}
}

func TestDueIsTheEarliestReportedDeadline(t *testing.T) {
	r := newRig(t, stalledFor(t))
	r.put(s1, "a", "session.stalled", workflow.Temporal{Kind: workflow.TemporalAfter, Time: t0, Threshold: 3 * time.Hour},
		"session.waited", workflow.Temporal{Kind: workflow.TemporalSince, Time: t0.Add(-time.Hour)},
		"session.answer_by", workflow.Temporal{Kind: workflow.TemporalAt, Time: t0.Add(2 * time.Hour)})
	r.put(s2, "b", "session.stalled", workflow.Temporal{Kind: workflow.TemporalAfter, Time: t0.Add(-time.Hour), Threshold: 2*time.Hour + time.Minute,
		WorkingDays: true}, "session.waited", workflow.Temporal{Kind: workflow.TemporalSince, Time: t0})
	r.sweep()
	// t0 is a Wednesday: s2's working-day threshold ends that day.
	if got, want := r.e.Due(), t0.Add(time.Hour+time.Minute+time.Nanosecond); !got.Equal(want) {
		t.Fatalf("Due %v, want s2's deadline %v", got, want)
	}
	r.clock.Set(r.e.Due())
	r.sweep()
	if got := r.ran("session.close"); len(got) != 1 || got[0] != s2 {
		t.Fatalf("ran %v at s2's deadline", got)
	}
	if got, want := r.e.Due(), t0.Add(2*time.Hour); !got.Equal(want) {
		t.Fatalf("Due %v, want s1's answer_by %v", got, want)
	}
	r.clock.Set(t0.Add(2 * time.Hour))
	r.sweep()
	if got, want := r.e.Due(), t0.Add(3*time.Hour+time.Nanosecond); !got.Equal(want) {
		t.Fatalf("Due %v, want s1's stalled %v", got, want)
	}
}

// countingSource is a source plugin that counts the host's calls after its
// reports.
type countingSource struct {
	*sourcetest.Stub
	calls atomic.Int32
}

func (c *countingSource) Notify(ctx context.Context, us []plugin.Unit, ts []plugin.Topic) error {
	c.calls.Add(1)
	return c.Stub.Notify(ctx, us, ts)
}

func (c *countingSource) Health(ctx context.Context) (plugin.Health, error) {
	c.calls.Add(1)
	return c.Stub.Health(ctx)
}

func (c *countingSource) Resolve(ctx context.Context, address string) (plugin.Unit, bool, error) {
	c.calls.Add(1)
	return c.Stub.Resolve(ctx, address)
}

func (c *countingSource) Locate(ctx context.Context, dir string) (plugin.Unit, bool, error) {
	c.calls.Add(1)
	return c.Stub.Locate(ctx, dir)
}

// reportedAfter reads facts as the plugin reported them and runs after once
// after the first read.
type reportedAfter struct {
	psource.UnitFacts
	after func()
	done  bool
}

func (r *reportedAfter) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	f, err := r.UnitFacts.Facts(ctx, u)
	if !r.done && r.after != nil {
		r.done = true
		r.after()
	}
	return f, err
}

func TestASweepReadsFactsWithoutCallingThePlugin(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	m := &manifest.Source{Name: "Acme", Codec: manifest.Codec{Keys: []string{"{name}"}}}
	c, err := codec.Of("acme", m.Codec)
	if err != nil {
		t.Fatal(err)
	}
	snaps := psource.NewSnapshots(db.Snapshots())
	broker := &psource.Broker{ID: "acme", Manifest: m, Snapshots: snaps, Units: psource.Units{Codecs: unit.NewRegistry(c)},
		Config: func() config.Config { return config.Config{} }}
	one := unit.Unit{Provider: "acme", Key: "one"}
	report := func(rev uint64, waiting time.Time) src.Snapshot {
		return src.Snapshot{Unit: plugin.Unit{Provider: "acme", Key: "one"}, Rev: rev, At: t0,
			Facts: []src.Fact{{Name: "acme.ready", Value: src.Bool(true)}, {Name: "acme.waiting", Value: src.Since(waiting)},
				{Name: "acme.late", Value: src.After(waiting, time.Hour, src.Calendar)}}}
	}
	stub := &countingSource{Stub: sourcetest.NewStub(report(1, t0.Add(-2*time.Hour)))}
	client := sourcetest.Launch(t, "acme", stub, broker.Host())
	ingest := func() {
		t.Helper()
		if _, err := client.Ingest(ctx, src.IngestRequest{Job: "ingest"}); err != nil {
			t.Fatal(err)
		}
	}
	ingest()

	var read workflow.Vars
	wf := workflow.Workflow{Name: "chase", Provider: "acme", Slot: "acme", Trigger: workflow.Trigger{Kind: workflow.TriggerSweep},
		When: automationtest.MatchFunc(func(v workflow.Vars) bool { read = v; return v.Facts["acme.ready"] == true },
			"acme.ready"), Steps: []workflow.Step{{Use: "session.close", Reach: reach["session.close"]}}}
	r := newRig(t, wf)
	units := &reportedAfter{UnitFacts: psource.UnitFacts{Snapshots: snaps}}
	r.e.Units = units
	before := stub.calls.Load()

	units.after = func() {
		stub.Script(report(2, t0.Add(-2*time.Hour)))
		ingest()
	}
	if sum := r.sweep(); len(r.steps.Ran()) != 0 || !strings.Contains(sum, "1 dropped") {
		t.Fatalf("a run planned on rev 1 ran after rev 2 was reported: %s, ran %v", sum, r.steps.Ran())
	}
	if read.Facts["acme.waiting"] != 2*time.Hour || read.Facts["acme.late"] != true {
		t.Errorf("the reported temporal facts read %v", read.Facts)
	}
	r.sweep()
	if got := r.ran("session.close"); len(got) != 1 || got[0] != one {
		t.Fatalf("ran %v on the next sweep", got)
	}
	if n := stub.calls.Load(); n != before {
		t.Errorf("the sweeps called the plugin %d times", n-before)
	}
	var _ automation.Revisioner = units
}
