package daemon

import (
	"net/http"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestActionsOmitDisabledPlugin(t *testing.T) {
	var on, off api.ActionsResponse
	if code := get(t, newServer(t), api.Actions, &on); code != http.StatusOK {
		t.Fatalf("GET %s: %d", api.Actions, code)
	}
	s := newServerWith(t, func(d *Deps) { d.PluginEnabled = githubOff })
	if code := get(t, s, api.Actions, &off); code != http.StatusOK {
		t.Fatalf("GET %s: %d", api.Actions, code)
	}
	var github int
	for _, sp := range on.Specs {
		if sp.Plugin == string(unit.GitHub) {
			github++
		}
	}
	if github == 0 {
		t.Fatal("no github action served with github enabled")
	}
	for _, sp := range off.Specs {
		if sp.Plugin == string(unit.GitHub) {
			t.Errorf("%s of the disabled github plugin is served", sp.ID)
		}
	}
	if len(off.Specs) != len(on.Specs)-github || off.Version == on.Version {
		t.Errorf("disabled: %d specs at %s; enabled: %d at %s", len(off.Specs), off.Version, len(on.Specs), on.Version)
	}
	var bindings api.KeysResponse
	get(t, s, api.Keys+"?surface=tui", &bindings)
	for _, b := range bindings.Bindings {
		if b.Owner == string(unit.GitHub) || b.Action == "review" || b.Action == "address" {
			t.Errorf("key %s of the disabled github plugin is served: %+v", b.Key, b)
		}
	}
}
