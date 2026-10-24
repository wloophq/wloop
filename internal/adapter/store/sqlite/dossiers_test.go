package sqlite

import (
	"context"
	"reflect"
	"testing"
	"time"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func init() {
	reads = append(reads, "DB.Dossiers", "dossierStore.Get", "dossierStore.List", "dossierStore.Open", "DB.AgentWorktrees")
	writes = append(writes, writeCase{
		method: "dossierStore.Put",
		call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Dossiers().Put(ctx, dossier.Dossier{Owner: dossier.Owner{Unit: ghPull}, RelPath: "github/acme/api%2312", Status: dossier.Active})
		},
	})
}

func TestDossierStoreRoundTrips(t *testing.T) {
	ctx := context.Background()
	db, path := openTemp(t)
	at := time.Date(2026, 10, 22, 12, 0, 0, 500, time.UTC)
	d := dossier.Dossier{Owner: dossier.Owner{Unit: unit.Unit{Provider: "slack", Key: "T000/C000/1.2"}}, RelPath: "slack/T000/C000/1.2",
		Status: dossier.Archived, Bytes: 10, Files: 2, LastWriteAt: at, ResolvedAt: at.Add(time.Hour), ArchivedAt: at.Add(2 * time.Hour), PurgeAfter: at.Add(3 * time.Hour)}
	p := dossier.Dossier{Owner: dossier.Owner{Project: "launch"}, RelPath: "project/launch", Status: dossier.Active}
	for _, x := range []dossier.Dossier{d, p} {
		if err := db.Dossiers().Put(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := db.Dossiers().Get(ctx, d.Owner)
	if err != nil || !ok || !reflect.DeepEqual(got, d) {
		t.Errorf("Get = %+v %v %v, want %+v", got, ok, err, d)
	}
	if _, ok, err := db.Dossiers().Get(ctx, dossier.Owner{Project: "none"}); ok || err != nil {
		t.Errorf("Get of an absent owner: %v %v", ok, err)
	}
	all, err := db.Dossiers().List(ctx)
	if err != nil || len(all) != 2 || all[0].Owner != p.Owner {
		t.Errorf("List = %+v %v", all, err)
	}
	if err := db.Dossiers().Put(ctx, dossier.Dossier{Owner: dossier.Owner{Unit: unit.Unit{Provider: "github", Key: "../x"}}}); err == nil {
		t.Error("Put accepted an owner with ..")
	}

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if all, err := ro.Dossiers().List(ctx); err != nil || len(all) != 2 {
		t.Errorf("read-only List = %d %v", len(all), err)
	}
	if err := ro.Dossiers().Put(ctx, p); err == nil {
		t.Error("a read-only store wrote")
	}
}

func TestDossierStoreOpenReadsItemsAndEntries(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	open := func(u unit.Unit) bool {
		t.Helper()
		ok, err := db.Dossiers().Open(ctx, dossier.Owner{Unit: u})
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if open(ghPull) {
		t.Fatal("a unit without items or entries is open")
	}
	it := sampleItem(ghPull, "i1", at)
	it.State, it.Resolution = items.Resolved, items.Done
	if err := db.Items().Commit(ctx, coreitems.Commit{Items: []items.Item{it}}); err != nil {
		t.Fatal(err)
	}
	if open(ghPull) {
		t.Fatal("a resolved item keeps the unit open")
	}
	it.State, it.Resolution = items.Open, ""
	if err := db.Items().Commit(ctx, coreitems.Commit{Items: []items.Item{it}}); err != nil {
		t.Fatal(err)
	}
	if !open(ghPull) {
		t.Fatal("an open item does not keep the unit open")
	}

	e := gtd.Entry{ID: gtd.NewID(), Kind: gtd.KindCapture, State: gtd.Next, Title: "t", Units: []unit.Unit{slackThread}, Origin: gtd.OriginUser, Created: at, Updated: at}
	if err := db.Entries().Apply(ctx, []gtd.Change{{Op: gtd.OpCreate, Entry: e}}); err != nil {
		t.Fatal(err)
	}
	if !open(slackThread) {
		t.Fatal("a live entry does not keep the unit open")
	}
	e.State = gtd.Done
	if err := db.Entries().Apply(ctx, []gtd.Change{{Op: gtd.OpUpdate, Entry: e}}); err != nil {
		t.Fatal(err)
	}
	if open(slackThread) {
		t.Fatal("a done entry keeps the unit open")
	}
	project := func() bool {
		t.Helper()
		ok, err := db.Dossiers().Open(ctx, dossier.Owner{Project: "ship-it"})
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if project() {
		t.Fatal("a project without an entry is open")
	}
	p := gtd.Entry{ID: gtd.NewID(), Kind: gtd.KindProject, State: gtd.Next, Title: "Ship it", Slug: "ship-it", Origin: gtd.OriginUser, Created: at, Updated: at}
	if err := db.Entries().Apply(ctx, []gtd.Change{{Op: gtd.OpCreate, Entry: p}}); err != nil {
		t.Fatal(err)
	}
	if !project() {
		t.Fatal("a live project is not open")
	}
	p.State = gtd.Done
	if err := db.Entries().Apply(ctx, []gtd.Change{{Op: gtd.OpUpdate, Entry: p}}); err != nil {
		t.Fatal(err)
	}
	if project() {
		t.Fatal("a done project is open")
	}
}

func TestAgentWorktreesListsEachOnce(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, a := range []AgentSession{
		{ID: "s1", Kind: "claude", Home: "/h", Worktree: "/w/a", FirstSeen: at},
		{ID: "s2", Kind: "claude", Home: "/h", Worktree: "/w/a", FirstSeen: at},
		{ID: "s3", Kind: "claude", Worktree: "/w/b", FirstSeen: at},
		{ID: "s4", Kind: "claude", FirstSeen: at},
	} {
		if err := db.SaveAgentSession(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.AgentWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []AgentWorktree{{Home: "/h", Worktree: "/w/a"}, {Worktree: "/w/b"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AgentWorktrees = %+v, want %+v", got, want)
	}
}
