package daemon

import (
	"errors"
	"strings"
	"testing"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestAUnitIntentRefusesWhatOnlyPullRequestsTake(t *testing.T) {
	t.Parallel()
	th := unit.Unit{Provider: "thread", Key: "1700000000.1"}
	offered := dintent.Def{Name: "discuss", Provider: "thread", Action: &dintent.Action{}}
	if err := unitIntent(offered, th); err != nil {
		t.Fatalf("an offered thread intent: %v", err)
	}
	unoffered := offered
	unoffered.Action = nil
	var c conflict
	if err := unitIntent(unoffered, th); !errors.As(err, &c) || c.msg != NotOffered("discuss", th) {
		t.Errorf("an intent offered on no surface: %v", err)
	}
	for what, edit := range map[string]func(*dintent.Def){
		"prepare": func(d *dintent.Def) { d.Prepare = "nudge.prepare" },
		"on_sent": func(d *dintent.Def) { d.OnSent = []string{"plan.drop_pending", "event.round_sent"} },
		"intake":  func(d *dintent.Def) { d.Intake = true },
		"launch":  func(d *dintent.Def) { d.Launch = "probe" },
	} {
		d := offered
		edit(&d)
		var bad badRequest
		if err := unitIntent(d, th); !errors.As(err, &bad) || !strings.Contains(bad.msg, what) || !strings.Contains(bad.msg, "pull requests only") {
			t.Errorf("%s: %v", what, err)
		}
	}
	github := dintent.Def{Name: "review"}
	if err := unitIntent(github, th); err == nil || !strings.Contains(err.Error(), "serves github units") {
		t.Errorf("a github intent on a thread: %v", err)
	}
}
