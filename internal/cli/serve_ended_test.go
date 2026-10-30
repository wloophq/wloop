package cli

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestAQuestionOfAPullRequestThatEndsLeavesTheInbox(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.db")
	seededStore(t, path, []seedTurn{askedTurn(1), askedTurn(2), askedTurn(3), askedTurn(4)})
	log := change.NewLog(change.DefaultWindow, t.Logf)
	db, err := sqlitetest.Open(path, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	store := inventory.New(db)
	save := func(n int, state string) {
		t.Helper()
		p := dinv.PR{Ref: pr.Ref{Owner: "acme", Repo: "api", Number: n}, Title: "t", Author: "ana", State: state, Reasons: []string{"author"},
			URL: "https://github.com/acme/api/pull/1", CreatedAt: testNow.Add(-48 * 3600e9), UpdatedAt: testNow, LastSeenAt: testNow}
		if state == "MERGED" {
			p.MergedAt = testNow
		}
		if err := inventorytest.Of(store).UpsertPR(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	save(1, "OPEN")
	save(2, "OPEN")
	save(4, "CLOSED")
	te := newTestEnv(t, &fake.Manager{})
	te.Env.reports = func(*inventory.Store) readmodel.Reports {
		return ghreporttest.Inventory{Store: store, Now: func() time.Time { return testNow }}
	}
	d := servedMessages(t, te, db, log)
	open := waitOpen(t, d, func(ms []message.Message) bool {
		return len(ms) == 3 && !slices.ContainsFunc(ms, func(m message.Message) bool { return m.TaskID == "" })
	})
	if open[2].Unit != ghUnit(3) {
		t.Fatalf("a closed pull request's question opened: %+v", open)
	}
	save(1, "MERGED")
	open = waitOpen(t, d, func(ms []message.Message) bool { return len(ms) == 2 })
	if open[0].Unit != ghUnit(2) || open[1].Unit != ghUnit(3) {
		t.Fatalf("open %+v", open)
	}
	if m, _, _ := d.Messages.Store.Message(ctx, 1); m.Unit != ghUnit(1) || m.Open() {
		t.Fatalf("the merged one's message %+v", m)
	}
	open1, err := d.GateReader.Where(ctx, gates.Query{Kind: gate.KindQuestion})
	if err != nil || len(open1) != 2 || slices.ContainsFunc(open1, func(g gate.Gate) bool { return g.Subject == "1" }) {
		t.Fatalf("open question gates %+v %v", open1, err)
	}
}
