package wails

import (
	"strings"
	"testing"
)

func ptBR(key string, params map[string]any) string {
	words := map[string]string{
		"Hide {name}": "Ocultar {name}", "Hide Others": "Ocultar outros", "Show All": "Mostrar tudo", "Quit {name}": "Encerrar {name}",
		"Edit": "Editar", "Undo": "Desfazer", "Redo": "Refazer", "Cut": "Recortar", "Copy": "Copiar", "Paste": "Colar", "Select All": "Selecionar tudo",
		"Window": "Janela", "Minimize": "Minimizar", "Zoom": "Zoom", "Close": "Fechar",
	}
	text, ok := words[key]
	if !ok {
		return "MISSING " + key
	}
	for k, v := range params {
		text = strings.ReplaceAll(text, "{"+k+"}", v.(string))
	}
	return text
}

func titles(items []menuItem) []string {
	var out []string
	for _, it := range items {
		if it.role != roleSeparator {
			out = append(out, it.title)
		}
		out = append(out, titles(it.items)...)
	}
	return out
}

func TestMenuTitlesComeFromTheCatalog(t *testing.T) {
	got := strings.Join(titles(menuOf("Acme", ptBR)), ", ")
	want := "Acme, Ocultar Acme, Ocultar outros, Mostrar tudo, Encerrar Acme, Editar, Desfazer, Refazer, Recortar, Copiar, Colar, Selecionar tudo, Janela, Minimizar, Zoom, Fechar"
	if got != want {
		t.Fatalf("menu titles\n got %s\nwant %s", got, want)
	}
}

func TestMenuOffersNoDeveloperTools(t *testing.T) {
	for _, title := range titles(menuOf("Acme", func(k string, _ map[string]any) string { return k })) {
		if strings.Contains(title, "Reload") || strings.Contains(title, "Developer") {
			t.Errorf("the menu offers %q", title)
		}
	}
}
