package web

import (
	"strings"
	"testing"
)

func TestEventLabelReadsTheCatalog(t *testing.T) {
	var got struct {
		Label, Unknown string
		Loop, Other    bool
	}
	nodeRun(t, jsConst(t, appJS(t), "catalog"), []string{"applyCatalog", "fieldOf", "eventLabel", "loopEvent"},
		`applyCatalog({ events: [{ provider: "", kind: "held", label: "pausado", loop: true }] });
process.stdout.write(JSON.stringify({ label: eventLabel("held"), loop: loopEvent("held"), unknown: eventLabel("some_kind"), other: loopEvent("some_kind") }));`, nil, &got)
	if got.Label != "pausado" || !got.Loop || got.Unknown != "some kind" || got.Other {
		t.Fatalf("%+v", got)
	}
	for _, gone := range []string{"EVENT_LABEL", "LOOP_EVENTS"} {
		if strings.Contains(appJS(t), gone) {
			t.Errorf("app.js still has %s; the core's event kinds are timeline.toml's", gone)
		}
	}
}
