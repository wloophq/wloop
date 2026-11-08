package profile

import (
	"testing"
)

// pluginChain is bundled, two plugins that both word ui.toml's queue.label differently, the user, and the user's
// layer of alpha holding userAlpha.
func pluginChain(t *testing.T, userAlpha map[string]string) []Layer {
	t.Helper()
	alpha := map[string]string{
		"ui.toml":               "[queue]\nlabel = \"Ready\"\n\n[noun]\nlabel = \"item\"\n",
		"locales/pt-BR/ui.toml": "[queue]\nlabel = \"Pronto\"\n\n[noun]\nlabel = \"item\"\n",
	}
	beta := map[string]string{
		"ui.toml":               "[queue]\nlabel = \"Close\"\n",
		"locales/pt-BR/ui.toml": "[queue]\nlabel = \"Fechar\"\n",
	}
	u := dir(t, map[string]string{})
	ua := dir(t, userAlpha)
	return []Layer{bundled(), {Name: PluginLayer("alpha"), Source: "alpha", FS: mapFS(alpha)}, {Name: PluginLayer("beta"), Source: "beta", FS: mapFS(beta)},
		user(u), {Name: "user:alpha", Source: ua, Dir: ua}}
}

func TestPluginLayersResolveInTheirOwnChain(t *testing.T) {
	copied := "[queue]\nlabel = \"Ready\"\n\n[noun]\nlabel = \"item\"\n"
	layers := pluginChain(t, map[string]string{"ui.toml": copied})
	x := NewTexts(layers, mapFS(bundledActions), "pt-BR", textKeys)
	out, problems := x.Localize(layers[4], "ui.toml", []byte(copied))
	if len(problems) > 0 {
		t.Fatalf("problems %v", problems)
	}
	if want := "[queue]\nlabel = \"Pronto\"\n\n[noun]\nlabel = \"item\"\n"; string(out) != want {
		t.Fatalf("a copy of alpha's ui.toml in alpha's user layer reads\n%s\nwant alpha's translation, never beta's", out)
	}
	beta, _ := x.Localize(layers[2], "ui.toml", []byte("[queue]\nlabel = \"Close\"\n"))
	if string(beta) != "[queue]\nlabel = \"Fechar\"\n" {
		t.Fatalf("beta reads %s", beta)
	}
	if f, ok := x.PluginForms("alpha", "ui.toml", []string{"queue", "label"}); !ok || f["other"] != "Pronto" {
		t.Fatalf("alpha's forms %v %v", f, ok)
	}
	if f, ok := x.PluginForms("beta", "ui.toml", []string{"queue", "label"}); !ok || f["other"] != "Fechar" {
		t.Fatalf("beta's forms %v %v", f, ok)
	}
}

func TestAWholeFileResolvesWhateverPluginTheTopLayerIs(t *testing.T) {
	alpha := map[string]string{
		"workflows/alpha-nudge/text.md.tmpl":               "Hi",
		"locales/pt-BR/workflows/alpha-nudge/text.md.tmpl": "Oi",
	}
	ua, ub := dir(t, map[string]string{"ui.toml": "[queue]\nlabel = \"x\"\n"}), dir(t, map[string]string{"ui.toml": "[queue]\nlabel = \"y\"\n"})
	layers := []Layer{bundled(), {Name: PluginLayer("alpha"), Source: "alpha", FS: mapFS(alpha)}, {Name: PluginLayer("beta"), Source: "beta", FS: mapFS(map[string]string{})},
		user(dir(t, map[string]string{})), {Name: "user:alpha", Source: ua, Dir: ua}, {Name: "user:beta", Source: ub, Dir: ub}}
	x := NewTexts(layers, mapFS(bundledActions), "pt-BR", textKeys)
	if r, err := x.File("workflows/alpha-nudge/text.md.tmpl"); err != nil || string(r.Body) != "Oi" {
		t.Fatalf("alpha's template with the user's beta layer on top: %q %v", r.Body, err)
	}
	if r, err := x.FileAt(layers[5], "workflows/alpha-nudge/text.md.tmpl"); err != nil || string(r.Body) != "Oi" {
		t.Fatalf("read at the user's beta layer: %q %v", r.Body, err)
	}
	if s, ok := x.Text("actions.toml", []string{"pause", "label"}); !ok || s != "Pausar" {
		t.Fatalf("a bundled text with a plugin's user layer on top: %q %v", s, ok)
	}
}

func TestAPluginUserTextOwnsItsKey(t *testing.T) {
	layers := pluginChain(t, map[string]string{"ui.toml": "[queue]\nlabel = \"Ship\"\n"})
	x := NewTexts(layers, mapFS(bundledActions), "pt-BR", textKeys)
	out, _ := x.Localize(layers[4], "ui.toml", []byte("[queue]\nlabel = \"Ship\"\n"))
	if string(out) != "[queue]\nlabel = \"Ship\"\n" {
		t.Fatalf("the user's own text reads %s", out)
	}
}
