package daemon

import (
	"errors"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// forgeCodec is an invented provider whose units have web addresses and
// whose keys compare without case.
type forgeCodec struct{}

func (forgeCodec) Provider() unit.ProviderID { return "forge" }
func (forgeCodec) Valid(key string) error {
	if key == "" {
		return errors.New("empty")
	}
	return nil
}
func (forgeCodec) Equal(a, b string) bool { return strings.EqualFold(a, b) }
func (forgeCodec) URL(key string) string  { return "https://forge.example/" + key }
func (forgeCodec) KeyOf(raw string) (string, bool) {
	return strings.CutPrefix(raw, "https://forge.example/")
}

func TestTheDaemonAsksCapabilitiesNotProviderIDs(t *testing.T) {
	t.Parallel()
	s := New(Deps{Units: unit.NewRegistry(forgeCodec{}, unit.OpaqueCodec{ID: "chat"}), Plugins: []api.Plugin{
		{ID: "forge", Enabled: true, Capabilities: []string{api.CapabilityCheckouts}},
		{ID: "chat", Enabled: true},
	}})

	u, err := s.parseUnitArg("https://forge.example/acme/7")
	if err != nil || u != (unit.Unit{Provider: "forge", Key: "acme/7"}) {
		t.Fatalf("by address: %+v %v", u, err)
	}
	if got := s.unitArg(u); got != "https://forge.example/acme/7" {
		t.Errorf("a unit with an address is named by it: %s", got)
	}
	if _, err := s.parseUnitArg("forge:acme/7"); err == nil {
		t.Error("a unit with an address is named by its address only")
	}
	chat, err := s.parseUnitArg("chat:C1/2")
	if err != nil || chat != (unit.Unit{Provider: "chat", Key: "C1/2"}) || s.unitArg(chat) != "chat:C1/2" {
		t.Fatalf("by provider and key: %+v %v", chat, err)
	}

	if !s.sameUnit(u, unit.Unit{Provider: "forge", Key: "ACME/7"}) {
		t.Error("forge keys compare through its codec")
	}
	if s.sameUnit(chat, unit.Unit{Provider: "chat", Key: "c1/2"}) {
		t.Error("chat keys compare byte for byte")
	}

	if !s.can("forge", api.CapabilityCheckouts) || s.can("chat", api.CapabilityCheckouts) {
		t.Fatal("capabilities come from the plugins the daemon serves")
	}
	s.deps.Plugins[0].Enabled = false
	if s.can("forge", api.CapabilityCheckouts) {
		t.Error("a disabled plugin serves no capability")
	}
}

func TestASessionOnAUnitWithItsOwnCheckoutAsksNoPlacer(t *testing.T) {
	t.Parallel()
	r, _, _ := newDelegateRig(t)
	r.put(t, unitTask("t-a", task.OriginDerived, actThread))
	r.s.deps.Placer = placerFunc(func(unit.Unit) (string, error) { return "", errors.New("no workspace manager is enabled") })
	modes := func() []api.RunnerState {
		var v api.TaskView
		if code := get(t, r.s, api.TaskPath("t-a"), &v); code != 200 {
			t.Fatalf("%d", code)
		}
		return verbState(v, "delegate").Runners
	}
	if got := modes(); len(got) != 2 || got[1].Enabled {
		t.Fatalf("placer refuses: %+v", got)
	}
	r.s.deps.Plugins = append(r.s.deps.Plugins, api.Plugin{ID: actThread.Provider, Enabled: true, Capabilities: []string{api.CapabilityCheckouts}})
	if got := modes(); len(got) != 2 || !got[1].Enabled {
		t.Fatalf("its own checkout: %+v", got)
	}
}
