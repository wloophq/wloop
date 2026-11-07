package daemon

import (
	"net/http"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/i18n"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

func TestGetStringsServesTheLoadedLocale(t *testing.T) {
	t.Parallel()
	base, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	pt, _ := intentload.LoadIn("pt-BR", profile.BundledChain(), turn.BundledFS(), base, turn.CheckTemplate)
	pt.Strings = map[string]i18n.Forms{"Open cockpit": {"other": "Abrir o cockpit"}, "{n} drafts": {"one": "{n} rascunho", "other": "{n} rascunhos"}}
	s := newServerWith(t, func(d *Deps) { d.Intents = func() *intentload.Loaded { return pt } })
	var got api.Strings
	if code := get(t, s, api.StringsPath, &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got.Locale != "pt-BR" || got.Strings["Open cockpit"]["other"] != "Abrir o cockpit" || got.Strings["{n} drafts"]["one"] != "{n} rascunho" ||
		got.Formats["time"] != "15:04" || got.Formats["group"] != "." || got.Stamp == "" || got.Stamp != pt.StringsStamp {
		t.Fatalf("%+v", got)
	}
	var en api.Strings
	get(t, newServer(t), api.StringsPath, &en)
	if en.Locale != "en" || en.Formats["time"] != "3:04 PM" || en.Strings == nil {
		t.Fatalf("%+v", en)
	}
}

func TestCatalogServesCoreEventLabels(t *testing.T) {
	t.Parallel()
	var got api.CatalogResponse
	get(t, newServer(t), api.Catalog, &got)
	var sent, held bool
	for _, e := range got.Events {
		sent = sent || e == api.EventSpec{Kind: "turn_sent", Label: "turn sent", Loop: true}
		held = held || e.Provider == "" && e.Kind == "held" && e.Label == "paused"
	}
	if !sent || !held {
		t.Fatalf("%+v", got.Events)
	}
}
