package cli

import (
	"context"
	"encoding/json"
	"net/http"
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
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTheComposedDaemonResolvesAPastedPullRequest(t *testing.T) {
	t.Parallel()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	held := unit.Unit{Provider: unit.GitHub, Key: "acme/team-docs#257"}
	if err := db.Tasks().Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: task.Task{ID: "t-0000000000000257", State: "next",
		Category: task.CategoryReady, Title: "review the docs", Units: []unit.Unit{held}, Origin: task.OriginUser, Created: daemonNow, Updated: daemonNow}}}); err != nil {
		t.Fatal(err)
	}
	h := daemon.New(withRows(daemon.Deps{Cfg: Defaults(), Version: "test", Now: clockFunc(func() time.Time { return daemonNow }), Log: t.Logf,
		Tasks: coretasks.New(bundledLens(coretasks.Deps{States: taskstest.BundledStates, Store: db.Tasks(), Clock: clockFunc(func() time.Time { return daemonNow })}))},
		inventory.New(db))).Handler()

	free := resolveText(t, h, "https://github.com/acme/api/pull/7")
	if want := (unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}); free.Kind != "unit" || free.Unit == nil || free.Unit.Unit != want || len(free.Unit.Tasks) != 0 {
		t.Fatalf("a pull request no task holds: %+v", free)
	}
	byURL := resolveText(t, h, "https://github.com/acme/team-docs/pull/257")
	byLabel := resolveText(t, h, "team-docs#257")
	for name, got := range map[string]api.OmniboxResolved{"its URL": byURL, "its label": byLabel} {
		if got.Kind != "unit" || got.Unit == nil || got.Unit.Unit != held || len(got.Unit.Tasks) != 1 || got.Unit.Tasks[0].ID != "t-0000000000000257" {
			t.Errorf("a held pull request by %s: %+v", name, got)
		}
	}
}

func resolveText(t *testing.T, h http.Handler, text string) api.OmniboxResolved {
	t.Helper()
	body, _ := json.Marshal(api.OmniboxText{Text: text})
	req := httptest.NewRequest("POST", api.OmniboxResolvePath, strings.NewReader(string(body)))
	req.Host = "127.0.0.1:47000"
	req.Header.Set(api.Header, api.Version)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var got api.OmniboxResolved
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%q: %d %s", text, rec.Code, rec.Body)
	}
	return got
}
