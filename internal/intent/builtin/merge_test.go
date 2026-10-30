package builtin

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// sourceOf parses a source plugin id's manifest whose [source] table ends
// with extra.
func sourceOf(t *testing.T, id, slot, extra string) *manifest.Source {
	t.Helper()
	m, err := manifest.Parse([]byte("id = \"" + id + "\"\nkinds = [\"source\"]\nprotocol = 1\n[source]\nname = \"N\"\nslot = \"" + slot +
		"\"\ncodec = {keys = [\"{name}\"]}\n" + extra))
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Source()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func role(key, value string) string {
	return "[[source.roles]]\nname = \"acme.round\"\ndoc = \"d\"\n" + key + " = \"" + value + "\"\n"
}

func TestMergeRefusesWhatTheHostCannotRun(t *testing.T) {
	other := "[[source.roles]]\nname = \"wiki.role\"\ndoc = \"d\"\n[[source.events]]\nname = \"wiki_edited\"\ndoc = \"d\"\n" +
		"[[source.tools]]\nname = \"wiki_read\"\ndoc = \"d\"\ninput = \"{}\"\n"
	for _, c := range []struct {
		name, id, slot, extra, want string
	}{
		{"a round kind", "acme", "acme", role("round_kind", "triage"), `round_kind "triage"`},
		{"a reconcile", "acme", "acme", role("reconcile", "theirs"), `reconcile "theirs"`},
		{"a prepare", "acme", "acme", role("prepare", "ask.prepare"), `prepare "ask.prepare"`},
		{"a launch", "acme", "acme", role("launch", "rocket"), `launch "rocket"`},
		{"a context", "acme", "acme", role("context", "dream"), `context "dream"`},
		{"a policy", "acme", "acme", role("policy", "lax"), `policy "lax"`},
		{"a workspace", "acme", "acme", role("workspace", "attic"), `workspace "attic"`},
		{"an unknown step", "acme", "acme", "steps = [\"turn.dismis\"]\n", "step turn.dismis"},
		{"a step of any unit", "acme", "acme", "steps = [\"turn.start\"]\n", "step turn.start"},
		{"an unknown config key", "acme", "acme", "config_reads = [\"swep\"]\n", `config_reads "swep"`},
		{"a core provider", "task", "acme", "", "core provider"},
		{"a core slot", "acme", "session", "", `slot "session"`},
		{"another plugin's slot, and so its facts", "acme", "wiki", "[[source.facts]]\nname = \"wiki.x\"\ntype = \"bool\"\ndoc = \"d\"\n", `slot "wiki" is plugin wiki's too`},
		{"another plugin's role", "acme", "acme", "[[source.roles]]\nname = \"wiki.role\"\ndoc = \"d\"\n", "role wiki.role is plugin wiki's too"},
		{"another plugin's event", "acme", "acme", "[[source.events]]\nname = \"wiki_edited\"\ndoc = \"d\"\n", "event wiki_edited is plugin wiki's too"},
		{"another plugin's tool", "acme", "acme", "[[source.tools]]\nname = \"wiki_read\"\ndoc = \"d\"\ninput = \"{}\"\n", "tool wiki_read is plugin wiki's too"},
		{"a core tool", "acme", "acme", "[[source.tools]]\nname = \"turn_end\"\ndoc = \"d\"\ninput = \"{}\"\n", "tool turn_end is a core tool"},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, err := Merge(sourceOf(t, "wiki", "wiki", other), sourceOf(t, c.id, c.slot, c.extra))
			if err == nil || !strings.Contains(err.Error(), "plugin "+c.id) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Merge = %v, want an error of plugin %s naming %s", err, c.id, c.want)
			}
			if v.slots["wiki"] != "wiki" || (c.id != "task" && v.slots[unit.ProviderID(c.id)] != "") {
				t.Errorf("slots %v: the refused source merged, or the good one did not", v.slots)
			}
		})
	}
}
