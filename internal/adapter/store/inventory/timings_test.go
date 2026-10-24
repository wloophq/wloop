package inventory

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func TestTimingsReadsTheWindow(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	since := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	before, in := since.Add(-time.Hour), since.Add(time.Hour)
	old := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	if err := s.SetMeta(ctx, MetaTimingsSince, ts(since.Add(-24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPR(ctx, dinv.PR{Ref: ref, Title: "T", Author: "ana", State: "OPEN", CreatedAt: before}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvents(ctx, []dinv.Event{
		{PR: ref, Kind: dinv.KindOpened, Actor: "ana", At: before},
		{PR: ref, Kind: dinv.KindReview, Key: "r1", Actor: "me", Data: "APPROVED:c1", At: in},
		{PR: old, Kind: dinv.KindOpened, Actor: "bob", At: before},
		{Kind: "brief_breaker_opened", Key: ts(in), At: in, Data: "529"},
	}); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		sqlitetest.Exec(t, s.db, q, args...)
	}
	exec(`INSERT INTO turns (seq, provider, unit_key, intent, turn_id, trigger, state, sent_at, started_at, ended_at, agent) VALUES (1, 'github', 'acme/api#7', 'review', 'a', 'automated', 'done', ?, ?, ?, 'claude')`, ts(in), ts(in.Add(time.Minute)), ts(in.Add(10*time.Minute)))
	exec(`INSERT INTO turns (seq, provider, unit_key, intent, turn_id, trigger, state, sent_at) VALUES (2, 'github', 'acme/api#7', 'review', 'b', 'automated', 'done', ?)`, ts(before))
	exec(`INSERT INTO turn_usage (turn_seq, model, messages, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, first_at, last_at) VALUES (1, 'opus', 3, 10, 20, 30, 40, NULL, ?, ?)`, ts(in), ts(in))
	exec(`INSERT INTO turn_usage (turn_seq, model, messages, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, first_at, last_at) VALUES (2, 'opus', 1, 1, 1, 1, 1, 0.5, ?, ?)`, ts(before), ts(before))
	exec(`INSERT INTO intakes (provider, unit_key, intent, source, request, state, accepted_at, starting_at) VALUES ('github', 'acme/api#7', 'review', 'cli', '{}', 'started', ?, ?)`, ts(in), ts(in.Add(14*time.Second)))
	id, err := s.StartBriefRun(ctx, dinv.BriefRun{PR: ref, Trigger: "wanted", StartedAt: in})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EndBriefRun(ctx, dinv.BriefRun{ID: id, PR: ref, Trigger: "wanted", StartedAt: in, EndedAt: in.Add(13 * time.Second), Outcome: "ok", Tags: []string{"change:fix"}, Files: []string{"api/export.go"}}, dinv.BriefRecord{PR: ref}); err != nil {
		t.Fatal(err)
	}

	d, err := s.Timings(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if !d.RecordedSince.Equal(since.Add(-24*time.Hour)) || d.Outages != 1 || len(d.Runs) != 1 {
		t.Fatalf("recorded %v outages %d runs %d", d.RecordedSince, d.Outages, len(d.Runs))
	}
	if len(d.Turns) != 1 || d.Turns[0].Seq != 1 || !d.Turns[0].EndedAt.Equal(in.Add(10*time.Minute)) || d.Turns[0].Agent != "claude" {
		t.Fatalf("turns %+v", d.Turns)
	}
	if len(d.Tokens) != 1 || d.Tokens[0].Output != 20 || d.Tokens[0].CostUSD != nil {
		t.Fatalf("tokens %+v", d.Tokens)
	}
	if len(d.Intakes) != 1 || !d.Intakes[0].StartingAt.Equal(in.Add(14*time.Second)) {
		t.Fatalf("intakes %+v", d.Intakes)
	}
	if len(d.PRs) != 1 || d.PRs[0].Ref != ref || len(d.Events[ref]) < 2 {
		t.Fatalf("prs %+v events %+v", d.PRs, d.Events)
	}
	if len(d.Tags) != 1 || d.Tags[0] != (dinv.TagTally{Key: "change", Value: "fix", Count: 1}) {
		t.Fatalf("tags %+v", d.Tags)
	}
	if !slices.Equal(d.PRTags[ref], []string{"change:fix"}) || !slices.Equal(d.PRFiles[ref], []string{"api/export.go"}) {
		t.Fatalf("pr tags %v files %v", d.PRTags, d.PRFiles)
	}
}

func TestTimingsReadsTurnsAsTheTurnStoreWritesThem(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	sent := time.Date(2026, 10, 18, 8, 10, 9, 717290000, time.UTC)
	if _, err := sqlite.New(s.db).Save(ctx, turn.Book{Unit: pr.Ref(ref).Unit(), Turns: []turn.Turn{{Intent: turn.Intent(dintent.Review), ID: "r1", Trigger: turn.Automated, State: turn.Done,
		SentAt: sent, StartedAt: sent.Add(6500 * time.Millisecond), EndedAt: sent.Add(33 * time.Second), Agent: "claude"}}}); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.R().QueryRowContext(ctx, `SELECT sent_at FROM turns`).Scan(&raw); err != nil || raw != "2026-10-18T08:10:09.71729Z" {
		t.Fatalf("stored %q %v", raw, err)
	}
	d, err := s.Timings(ctx, sent.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Turns) != 1 || !d.Turns[0].SentAt.Equal(sent) || !d.Turns[0].EndedAt.Equal(sent.Add(33*time.Second)) {
		t.Fatalf("turns %+v", d.Turns)
	}
}

func TestTimingsBudget(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	since := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	in := since.Add(time.Hour)
	if err := s.SetMeta(ctx, MetaTimingsSince, ts(since.Add(-24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	const active = 227
	var refs []github.Ref
	var evs []dinv.Event
	for n := 1; n <= active; n++ {
		r := github.Ref{Owner: "acme", Repo: "api", Number: n}
		refs = append(refs, r)
		if n%10 != 0 {
			upsert(t, s, dinv.PR{Ref: r, Title: "T", Author: "ana", State: "OPEN", CreatedAt: in})
		}
		evs = append(evs, dinv.Event{PR: r, Kind: dinv.KindOpened, Actor: "ana", At: in})
	}
	if _, err := s.AppendEvents(ctx, evs); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartBriefRun(ctx, dinv.BriefRun{PR: refs[0], Trigger: "wanted", StartedAt: in}); err != nil {
		t.Fatal(err)
	}

	mctx, sc := metertest.Start(t, ctx)
	d, err := s.Timings(mctx, since)
	if err != nil {
		t.Fatal(err)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 14})

	if len(d.PRs) != active {
		t.Fatalf("%d pull requests, want %d", len(d.PRs), active)
	}
	for _, r := range refs {
		want, ok, err := s.PR(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			want = dinv.PR{Ref: r}
		}
		if got := d.PRs[r.Number-1]; !reflect.DeepEqual(got, want) {
			t.Fatalf("%v: got %+v, want %+v", r, got, want)
		}
	}
}
