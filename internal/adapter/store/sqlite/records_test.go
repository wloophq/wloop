package sqlite

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestATasksTimelineReadsTheStoreByRowID(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	id := task.ID("t-0123456789abcdef")
	pr := unit.Unit{Provider: "github", Key: "acme/api#7"}
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	exec(t, db, `INSERT INTO tasks (id, kind, state, origin, created, updated, rev) VALUES (?, 'action', 'next', 'user', '', '', 1)`, string(id))
	exec(t, db, `INSERT INTO task_units (task_id, provider, unit_key, ord) VALUES (?, 'github', 'acme/api#7', 0)`, string(id))
	for i, e := range []struct{ p, k, kind string }{
		{"task", string(id), "chat"}, {"github", "acme/api#7", "pushed"}, {"github", "acme/api#8", "pushed"}, {"task", string(id), "chat"},
	} {
		exec(t, db, `INSERT INTO events (provider, unit_key, kind, key, actor, at, data) VALUES (?, ?, ?, '', '', ?, 'x')`, e.p, e.k, e.kind, at.Add(time.Duration(i)*time.Second).Format("2006-01-02T15:04:05.000000000Z"))
	}
	if err := db.PutRecord(ctx, pr, "brief", task.WriterAgent, []byte(`{"goal":"one"}`), at); err != nil {
		t.Fatal(err)
	}
	if err := db.PutRecord(ctx, pr, "brief", task.WriterAgent, []byte(`{"goal":"two"}`), at); err != nil {
		t.Fatal(err)
	}
	r := timeline.Merged{Log: db, Holdings: db}
	got, err := r.Of(ctx, id, timeline.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, ev := range got {
		kinds = append(kinds, ev.Kind+" "+ev.Unit.Key)
	}
	if want := []string{"record acme/api#7", "chat " + string(id), "pushed acme/api#7", "chat " + string(id)}; !slices.Equal(kinds, want) {
		t.Fatalf("timeline %q, want %q", kinds, want)
	}
	if got[0].Data != `{"goal":"two"}` || got[0].Actor != "agent" || !got[0].At.Equal(at) || got[0].Seq <= got[1].Seq {
		t.Fatalf("the newest record: %+v", got[0])
	}
	page, _ := r.Of(ctx, id, timeline.Filter{Before: got[1].Seq, Limit: 1, Kinds: []string{"pushed", "chat"}})
	if len(page) != 1 || page[0].Kind != "pushed" {
		t.Fatalf("the page before %d: %+v", got[1].Seq, page)
	}
	recs, err := db.Records(ctx, "brief", pr, task.UnitOf(id))
	if err != nil || len(recs) != 1 || recs[pr].Data != `{"goal":"two"}` {
		t.Fatalf("records %+v %v", recs, err)
	}
}
