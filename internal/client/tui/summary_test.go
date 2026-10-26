package tui

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

func summaryOf(t *testing.T, id string) string {
	t.Helper()
	for _, s := range servedSpecs(t).Specs {
		if s.ID == id && s.Summary != "" {
			return s.Summary
		}
	}
	t.Fatalf("%s has no summary", id)
	return ""
}

func TestHelpListsWhatEachActionDoes(t *testing.T) {
	m, _ := modelOn(t, "blocked_untrusted")
	press(m, Key{Rune: '?'})
	screen := squash(screenOf(m, 400, 120))
	for _, id := range []string{"pause", "mute", "run_now"} {
		if want := summaryOf(t, id); !strings.Contains(screen, want) {
			t.Errorf("help lacks %s's summary %q:\n%s", id, want, screen)
		}
	}
}

func TestThePaletteSaysWhatTheFocusedActionDoes(t *testing.T) {
	m, r := modelOn(t, "blocked_untrusted")
	press(m, Key{Name: KeyCtrlK})
	at := -1
	for i, it := range m.overlay.items {
		if strings.HasPrefix(it.label, "Trust folder and run") {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("the palette offers no trust_run: %+v", m.overlay.items)
	}
	for range at {
		press(m, Key{Name: KeyDown})
	}
	var note string
	for _, st := range r.Actions {
		if st.ID == "trust_run" {
			note = st.Note
		}
	}
	screen := squash(screenOf(m, 400, 60))
	if want := summaryOf(t, "trust_run") + " · " + note; note == "" || !strings.Contains(screen, want) {
		t.Errorf("the palette's status lacks %q:\n%s", want, screen)
	}
}

func TestThePaletteSaysNothingMoreForAnActionWithoutSummary(t *testing.T) {
	m := modelWith(t, offering(fixtures(t)["blocked"], "pause"))
	m.SetSpecs([]api.ActionSpec{{ID: "pause", Label: "Pause", Key: "H", Surfaces: []string{"tui"}}})
	press(m, Key{Name: KeyCtrlK})
	if !strings.Contains(squash(screenOf(m, 200, 40)), "Enter runs, Esc closes") {
		t.Error("the palette lost its hint")
	}
}
