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
		got.Formats["time"] != "15:04" || got.Formats["group"] != "." {
		t.Fatalf("%+v", got)
	}
	var en api.Strings
	get(t, newServer(t), api.StringsPath, &en)
	if en.Locale != "en" || en.Formats["time"] != "3:04 PM" || en.Strings == nil {
		t.Fatalf("%+v", en)
	}
}
