package factsgithub_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/claudedesktop"
)

type psOnly struct{ ps string }

func (r psOnly) Run(_ context.Context, c claudedesktop.Cmd) ([]byte, error) {
	if c.Name == "ps" {
		return []byte(r.ps), nil
	}
	return nil, fmt.Errorf("unexpected %s", c)
}

func TestTheDesktopListingIsWholeWhetherTheAppRunsOrNot(t *testing.T) {
	const app = "/Applications/Claude.app"
	for name, ps := range map[string]string{
		"running": "21671 Fri Oct 23 20:56:30 2026     " + app + "/Contents/MacOS/Claude\n",
		"down":    "",
	} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			m := &claudedesktop.Manager{Root: filepath.Join(base, "worktrees"), SessionsDir: filepath.Join(base, "sessions"),
				StoreDir: filepath.Join(base, "store"), AppPath: app, Exec: psOnly{ps}, Now: time.Now}
			desktop, err := workspace.NewSet(workspace.NewCached(m, m, time.Minute)).Read(context.Background(), wsdomain.PartAll)
			if err != nil {
				t.Fatal(err)
			}

			w := newWorld()
			r := w.mine(5, nil)
			w.snap.Scopes = map[string]wsdomain.Scopes{
				"orca":             {Workspaces: complete, Terminals: complete},
				claudedesktop.Name: desktop.Scopes[claudedesktop.Name],
			}
			if f := w.facts(t, w.provider(), r.Unit()); f["pr.address.due"] != true {
				t.Fatalf("address = %v, %v with Desktop scopes %+v", f["pr.address.due"], f["pr.address.reason"], desktop.Scopes[claudedesktop.Name])
			}
		})
	}
}
