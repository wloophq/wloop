package unit_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workspace"
)

func TestKeysRoundTripThroughTheirCodec(t *testing.T) {
	reg := unit.NewRegistry(unit.OpaqueCodec{ID: "thread"}, turn.SessionCodec{}, workspace.Codec{})
	for _, u := range []unit.Unit{{Provider: "thread", Key: "C1 thread 9"}, {Provider: unit.Session, Key: "term_1"}, {Provider: unit.Workspace, Key: "wt-9"}} {
		back, err := reg.Parse(string(u.Provider), u.Key)
		if err != nil || back != u {
			t.Errorf("Parse(%v) = %v %v", u, back, err)
		}
	}
	if u, _ := reg.Parse("thread", "C1 thread 9"); u.String() != "thread:C1 thread 9" {
		t.Errorf("String = %q", u.String())
	}
	if _, err := reg.Parse("thread", "a\nb"); err == nil {
		t.Error("a key with a line break parsed")
	}
	if _, err := reg.Parse("slack", "C1 thread 9"); err == nil || err.Error() != "unknown provider slack" {
		t.Errorf("unregistered provider: %v", err)
	}
	if !slices.Equal(reg.Providers(), []unit.ProviderID{"session", "thread", "workspace"}) {
		t.Errorf("Providers = %v", reg.Providers())
	}
}

func TestUnitOrder(t *testing.T) {
	us := []unit.Unit{{Provider: "session", Key: "a"}, {Provider: "github", Key: "o/r#2"}, {Provider: "github", Key: "o/r#10"}, {}}
	slices.SortFunc(us, func(a, b unit.Unit) int {
		switch {
		case a.Less(b):
			return -1
		case b.Less(a):
			return 1
		}
		return 0
	})
	want := []unit.Unit{{}, {Provider: "github", Key: "o/r#10"}, {Provider: "github", Key: "o/r#2"}, {Provider: "session", Key: "a"}}
	if !slices.Equal(us, want) {
		t.Errorf("sorted = %v", us)
	}
	if !us[0].IsZero() || us[1].IsZero() {
		t.Error("IsZero")
	}
}

func TestValidProviderTakesALowercaseIDOfUpTo32Characters(t *testing.T) {
	for id, want := range map[string]bool{
		"github": true, "slack": true, "linear_v2": true, "a-b": true, "abcdefghijklmnopqrstuvwxyz012345": true,
		"": false, "Slack": false, "1slack": false, "slack:x": false, "abcdefghijklmnopqrstuvwxyz01234567": false,
	} {
		if got := unit.ValidProvider(id); got != want {
			t.Errorf("ValidProvider(%q) = %v, want %v", id, got, want)
		}
	}
}

type namedCodec struct{ unit.OpaqueCodec }

func (namedCodec) Target() string { return "<url>" }

func TestATargetIsHowACommandLineNamesAProvidersUnit(t *testing.T) {
	reg := unit.NewRegistry(namedCodec{unit.OpaqueCodec{ID: "forge"}}, unit.OpaqueCodec{ID: "thread"})
	for p, want := range map[unit.ProviderID]string{"forge": "<url>", "thread": "<thread:key>", "chat": "<chat:key>"} {
		if got := reg.Target(p); got != want {
			t.Errorf("Target(%s) = %q, want %q", p, got, want)
		}
	}
	if got := (*unit.Registry)(nil).Target("forge"); got != "<forge:key>" {
		t.Errorf("a nil registry's target %q", got)
	}
	for _, c := range []struct {
		ps   []unit.ProviderID
		want string
	}{
		{nil, "<url|thread:key>"},
		{[]unit.ProviderID{"thread"}, "<thread:key>"},
		{[]unit.ProviderID{"thread", "forge", "thread"}, "<thread:key|url>"},
	} {
		if got := reg.Targets(c.ps...); got != c.want {
			t.Errorf("Targets(%v) = %q, want %q", c.ps, got, c.want)
		}
	}
}

type linked struct{ unit.OpaqueCodec }

func (linked) URL(key string) string       { return "https://acme.example/" + key }
func (linked) KeyOf(string) (string, bool) { return "", false }

func TestAUnitIsAddressedByItsURLWhenItsCodecHasOne(t *testing.T) {
	unit.UseLabels(unit.NewRegistry(linked{unit.OpaqueCodec{ID: "acme"}}, unit.OpaqueCodec{ID: "thread"}))
	t.Cleanup(func() { unit.UseLabels(nil) })
	if got := (unit.Unit{Provider: "acme", Key: "42"}).Address(); got != "https://acme.example/42" {
		t.Errorf("a linked unit's address = %q", got)
	}
	if got := (unit.Unit{Provider: "thread", Key: "t1"}).Address(); got != "thread:t1" {
		t.Errorf("an unlinked unit's address = %q", got)
	}
}

// forge is a provider with addresses: its keys are repo#n, under https://forge.test/.
type forge struct{ id unit.ProviderID }

func (f forge) Provider() unit.ProviderID { return f.id }

func (forge) Valid(key string) error {
	if repo, n, ok := strings.Cut(key, "#"); !ok || repo == "" || n == "" || strings.ContainsAny(key, " /") {
		return fmt.Errorf("not a forge key: %q", key)
	}
	return nil
}

func (f forge) URL(key string) string {
	if f.Valid(key) != nil {
		return ""
	}
	return "https://forge.test/" + strings.Replace(key, "#", "/", 1)
}

func (forge) KeyOf(url string) (string, bool) {
	rest, ok := strings.CutPrefix(url, "https://forge.test/")
	return strings.Replace(rest, "/", "#", 1), ok && rest != ""
}

func TestAKeyAPersonTypesNamesTheUnitOfTheOneProviderWhoseAddressReadsItBack(t *testing.T) {
	reg := unit.NewRegistry(unit.OpaqueCodec{ID: "thread"}, forge{id: "forge"})
	if u, ok := reg.ParseKey("api#7"); !ok || u != (unit.Unit{Provider: "forge", Key: "api#7"}) {
		t.Fatalf("ParseKey = %v %v", u, ok)
	}
	for _, raw := range []string{"api", "call ana", "https://forge.test/api/7", ""} {
		if u, ok := reg.ParseKey(raw); ok {
			t.Errorf("ParseKey(%q) = %v", raw, u)
		}
	}
	two := unit.NewRegistry(forge{id: "forge"}, forge{id: "mirror"})
	if u, ok := two.ParseKey("api#7"); ok {
		t.Errorf("a key two providers take named %v", u)
	}
}
