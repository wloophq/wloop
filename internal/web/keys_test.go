package web

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/keys"
)

// webBindings is GET /api/keys?surface=web as the daemon serves it with the
// bundled actions.
func webBindings(t *testing.T) []api.KeyView {
	t.Helper()
	raw, err := os.ReadFile("testdata/keys.web.json")
	if err != nil {
		t.Fatal(err)
	}
	var r api.KeysResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r.Bindings
}

func TestKeysFixtureHoldsTheCoreWebMap(t *testing.T) {
	help, _ := intentload.BundledKeys(os.DirFS("../../profiles/default"))
	words, _ := intentload.BundledTasks(os.DirFS("../../profiles/default"))
	var want, got []api.KeyView
	for _, b := range keys.Core {
		if slices.Contains(b.Surfaces, keys.Web) {
			v := api.KeyView{Key: b.Key, Action: b.Action, Context: string(b.Context), Owner: "core", Help: help.Help(string(b.Context), b.Action)}
			if b.Context == keys.List {
				v.Summary = words.Verbs[b.Action].Summary
			}
			want = append(want, v)
		}
	}
	got = webBindings(t)[:len(want)]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("testdata/keys.web.json is stale: regenerate it from the daemon's keyViews(keys.Web, ...)")
	}
}
