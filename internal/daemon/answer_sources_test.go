package daemon

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/wloophq/wloop/internal/client/menubar"
	"github.com/wloophq/wloop/internal/client/tui"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// cockpitSource is the source every request of the cockpit names.
func cockpitSource(t *testing.T) string {
	t.Helper()
	js, err := os.ReadFile(filepath.Join("..", "web", "static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`const body = \{ source: "([a-z]+)" \};`).FindSubmatch(js)
	if m == nil {
		t.Fatal("app.js names no source in act's body")
	}
	return string(m[1])
}

func TestEveryClientsAnswerIsAccepted(t *testing.T) {
	t.Parallel()
	for client, source := range map[string]string{"menubar": menubar.AnswerSource, "tui": tui.Source, "cockpit": cockpitSource(t)} {
		r := newTurnRig(t)
		r.asked(pr.Ref{Owner: "acme", Repo: "api", Number: 8}, turn.Intent(dintent.Review), turn.Asked, publishQuestion)
		if rec := post(t, r.s, "/api/units/github/acme%2Fapi%238/answer", `{"option_id":"publish","source":"`+source+`"}`); rec.Code != 200 {
			t.Errorf("%s answers as %q: %d %s", client, source, rec.Code, rec.Body)
		}
	}
}
