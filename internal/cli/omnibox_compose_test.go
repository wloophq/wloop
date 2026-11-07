package cli

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTheComposedDaemonResolvesAPastedPullRequest(t *testing.T) {
	t.Parallel()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h := daemon.New(withRows(daemon.Deps{Cfg: Defaults(), Version: "test", Now: clockFunc(func() time.Time { return daemonNow }), Log: t.Logf,
		Tasks: coretasks.New(bundledLens(coretasks.Deps{States: taskstest.BundledStates, Store: db.Tasks(), Clock: clockFunc(func() time.Time { return daemonNow })}))},
		inventory.New(db))).Handler()
	body, _ := json.Marshal(api.OmniboxText{Text: r7.URL()})
	req := httptest.NewRequest("POST", api.OmniboxResolvePath, strings.NewReader(string(body)))
	req.Host = "127.0.0.1:47000"
	req.Header.Set(api.Header, api.Version)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var got api.OmniboxResolved
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if want := (unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}); got.Kind != "unit" || got.Unit == nil || got.Unit.Unit != want {
		t.Fatalf("%+v", got)
	}
}
