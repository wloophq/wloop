package cli

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestAPluginRefusedAtBuildCountsAsOff(t *testing.T) {
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	te := newTestEnv(t, &fake.Manager{})
	ctx := context.Background()
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	u := unit.Unit{Provider: "slack", Key: "C1/1.1"}
	e := gtd.Entry{ID: "e-00000000000000ab", Kind: gtd.KindAction, State: gtd.Inbox, Title: "reply", Units: []unit.Unit{u},
		Origin: gtd.OriginUser, Created: at, Updated: at}
	if err := db.Entries().Apply(ctx, []gtd.Change{{Op: gtd.OpCreate, Entry: e}}); err != nil {
		t.Fatal(err)
	}
	d := daemon.Deps{Plugins: []api.Plugin{{ID: unit.GitHub, Enabled: true}, {ID: "slack"}}}
	d = te.Env.serveGTD(d, db, te.cfg, nil, t.Logf)
	c, err := d.GTD.Counts(ctx, "")
	if err != nil || c.Views[gtd.ViewInbox] != 0 || c.Badge != 0 {
		t.Fatalf("counts %+v %v", c, err)
	}
	if _, err := d.GTD.Do(ctx, e.ID, coregtd.Request{Verb: gtd.VerbNext}); !errors.Is(err, coregtd.ErrPluginDisabled) {
		t.Fatalf("next on a refused plugin's entry: %v", err)
	}
}
