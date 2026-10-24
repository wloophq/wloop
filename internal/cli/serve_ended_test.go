package cli

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/domain/gtd"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestAQuestionOfAPullRequestThatEndsLeavesTheInbox(t *testing.T) {
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
		if err := store.UpsertPR(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	save(1, "OPEN")
	save(2, "OPEN")
	save(4, "CLOSED")
	te := newTestEnv(t, &fake.Manager{})
	d := servedMessages(t, te, db, log)
	open := waitOpen(t, d, func(ms []message.Message) bool {
		return len(ms) == 3 && !slices.ContainsFunc(ms, func(m message.Message) bool { return m.EntryID == "" })
	})
	if open[2].Unit != ghUnit(3) {
		t.Fatalf("a closed pull request's question opened: %+v", open)
	}
	save(1, "MERGED")
	open = waitOpen(t, d, func(ms []message.Message) bool { return len(ms) == 2 })
	if open[0].Unit != ghUnit(2) || open[1].Unit != ghUnit(3) {
		t.Fatalf("open %+v", open)
	}
	if m, _, _ := d.Messages.Store.Message(ctx, 1); m.Unit != ghUnit(1) || m.State != message.Superseded {
		t.Fatalf("the merged one's message %+v", m)
	}
	inbox, err := db.Entries().Entries(ctx, gtd.Query{View: gtd.ViewInbox})
	if err != nil || len(inbox) != 2 {
		t.Fatalf("inbox %+v %v", inbox, err)
	}
}
