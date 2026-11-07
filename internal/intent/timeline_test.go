package intent

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

func TestTimelineLabelsLoadPerLocale(t *testing.T) {
	pt, problems := LoadIn("pt-BR", bundledOnly(), bundledDefault(t), testBase(), nil)
	for _, p := range problems {
		if strings.Contains(p.File, TimelineFile) {
			t.Errorf("%v", p)
		}
	}
	en, _ := LoadIn("en", bundledOnly(), bundledDefault(t), testBase(), nil)
	if pt.Timeline.Events["held"].Label != "pausado" || en.Timeline.Events["held"].Label != "paused" || en.Timeline.Events["turn_sent"].Label != "turn sent" {
		t.Fatalf("%v %v", pt.Timeline.Events["held"], en.Timeline.Events["held"])
	}
	for _, k := range builtin.EventKinds {
		if en.Timeline.Events[k.Name].Label == "" || pt.Timeline.Events[k.Name].Label == "" {
			t.Errorf("%s has no label", k.Name)
		}
	}
}

func TestTimelineNamesOnlyKnownEventKinds(t *testing.T) {
	dir := userDir(t, map[string]string{TimelineFile: "[events.helt]\nlabel = \"x\"\n\n[events.held]\nlabel = \"on hold\"\n\n[events.muted]\nlabel = \"  \"\n"})
	l, problems := LoadIn("en", userLayer(dir), bundledDefault(t), testBase(), nil)
	var unknown, blank bool
	for _, p := range problems {
		unknown = unknown || p.Field == "events.helt" && strings.Contains(p.Msg, `did you mean "held"?`)
		blank = blank || p.Field == "events.muted.label" && strings.Contains(p.Msg, "is required")
	}
	if !unknown || !blank || l.Timeline.Events["held"].Label != "on hold" || l.Timeline.Events["muted"].Label != "muted" {
		t.Fatalf("%v %v", l.Timeline.Events, problems)
	}
}
