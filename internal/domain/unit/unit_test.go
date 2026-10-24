package unit_test

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workspace"
)

func registry() *unit.Registry {
	return unit.NewRegistry(pr.Codec{}, turn.SessionCodec{}, workspace.Codec{})
}

func TestKeysRoundTripThroughTheirCodec(t *testing.T) {
	ref := pr.Ref{Owner: "o", Repo: "r", Number: 12}
	got, err := pr.ParseKey("o/r#12")
	if err != nil || got != ref {
		t.Fatalf("ParseKey = %v %v", got, err)
	}
	if u := ref.Unit(); u.Provider != unit.GitHub || u.Key != "o/r#12" || u.String() != "github:o/r#12" {
		t.Fatalf("Unit = %+v", u)
	}
	reg := registry()
	for _, u := range []unit.Unit{ref.Unit(), {Provider: unit.Session, Key: "term_1"}, {Provider: unit.Workspace, Key: "wt-9"}} {
		back, err := reg.Parse(string(u.Provider), u.Key)
		if err != nil || back != u {
			t.Errorf("Parse(%v) = %v %v", u, back, err)
		}
	}
	if _, err := reg.Parse("github", "o/r#x"); err == nil {
		t.Error("a malformed github key parsed")
	}
	if _, err := reg.Parse("github", "o/r#012"); err == nil {
		t.Error("a non-canonical github key parsed")
	}
	if _, err := reg.Parse("slack", "C1 thread 9"); err == nil || err.Error() != "unknown provider slack" {
		t.Errorf("unregistered provider: %v", err)
	}
}

func TestAProviderRegistersWithoutCore(t *testing.T) {
	reg := unit.NewRegistry(pr.Codec{}, unit.OpaqueCodec{ID: "thread"})
	u, err := reg.Parse("thread", "C1 thread 9")
	if err != nil || u.String() != "thread:C1 thread 9" {
		t.Fatalf("Parse = %v %v", u, err)
	}
	if !slices.Equal(reg.Providers(), []unit.ProviderID{"github", "thread"}) {
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
