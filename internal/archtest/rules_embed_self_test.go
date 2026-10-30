package archtest

import "testing"

func TestREmbedFlagsProseEmbeddedOutsideProfiles(t *testing.T) {
	t.Parallel()
	used := map[int]bool{}
	flagged(t, "prose", ruleEmbed("internal/x", []string{"a.md", "t/b.tmpl", "n.txt", "schema.json", "img/i.png"}, nil, used), 3, "(R-embed)")
	flagged(t, "the profile", ruleEmbed("profiles", []string{"default/intents/x/instructions.md.tmpl"}, nil, used), 0, "")
	flagged(t, "a plugin's layer", ruleEmbed("plugins", []string{"acme/profile/prompts/p.md", "acme/notes.md", "acme/x/profile/p.md"}, nil, used), 2, "plugins/acme/")
	allow := []embedAllowed{{"internal/x/t", "a reason"}, {"internal/y/gone.md", "moved"}}
	flagged(t, "allowlisted", ruleEmbed("internal/x", []string{"a.md", "t/b.tmpl"}, allow, used), 1, "internal/x/a.md")
	if !used[0] || used[1] {
		t.Errorf("used %v", used)
	}
	flagged(t, "stale", staleEmbeds(allow, used), 1, "internal/y/gone.md")
}
