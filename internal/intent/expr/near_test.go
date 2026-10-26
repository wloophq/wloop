package expr

import "testing"

func TestClosestSuggestsATypo(t *testing.T) {
	t.Parallel()
	for typo, want := range map[string]string{
		"sesion": "session", "frist": "first", "stag": "stage", "dry-run": "dry_run", "session.clsoe": "session.close",
		"repeat_aftr": "repeat_after", "PUBLIC": "public", "pr.mien": "pr.mine", "reviw_due": "review_due",
		"session.manged": "session.managed",
	} {
		if got := Closest(typo, []string{want, "zzzzzzzz"}); got != want {
			t.Errorf("Closest(%q) = %q, want %q", typo, got, want)
		}
	}
}

// An old name lies farther from today's names than a typo does, so it
// gets no guess, which would point it at an unrelated key.
func TestClosestGuessesNothingForAnOldName(t *testing.T) {
	t.Parallel()
	for typo, options := range map[string][]string{
		"record":            {"send", "delay", "wait", "dry_run"},
		"key":               {"why", "label", "role"},
		"automated_per_day": {"per_day", "per_unit_per_day"},
		"mode":              {"order", "observe"},
		"modes":             {"role", "runners"},
		"reason":            {"session", "refuse"},
		"plugins":           {"turns", "profile"},
		"proposals":         {"profile", "projects_dir"},
		"project_settings":  {"projects_dir", "projects"},
		"draft_approval":    {"approval", "drafts"},
		"rule":              {"mode", "window"},
	} {
		if got := Closest(typo, options); got != "" {
			t.Errorf("Closest(%q, %v) = %q, want none", typo, options, got)
		}
	}
}

func TestClosestGuessesNothingOnATie(t *testing.T) {
	t.Parallel()
	if got := Closest("cat", []string{"bat", "hat", "bat"}); got != "" {
		t.Errorf("got %q", got)
	}
	if got := Closest("sesion", []string{"session", "session"}); got != "session" {
		t.Errorf("a repeated option is one option: %q", got)
	}
}
