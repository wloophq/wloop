package menubar

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
)

var ptBRStrings = api.Strings{Locale: "pt-BR", Strings: map[string]api.Text{
	"Open cockpit": {"other": "Abrir o cockpit"}, "Sweep now": {"other": "Varrer agora"}, "Quit {name}": {"other": "Sair do {name}"},
	"Answer not sent": {"other": "Resposta não enviada"}, "Synced {time}": {"other": "Sincronizado às {time}"},
}, Formats: map[string]string{"time": "15:04"}}

func (r *rig) menuTitles() []string {
	r.tray.mu.Lock()
	defer r.tray.mu.Unlock()
	return titles(r.tray.menu)
}

func TestMenuShowsPtBRWhenTheDaemonServesIt(t *testing.T) {
	r := newRig(t)
	s := ptBRStrings
	r.d.strings = &s
	r.c.Refresh(context.Background())
	got := r.menuTitles()
	for _, want := range []string{"Abrir o cockpit", "Varrer agora", "Sair do " + brand.Name, "Pause automatic rounds"} {
		if !slices.Contains(got, want) {
			t.Errorf("no %q in %q", want, got)
		}
	}
}

func TestMenuShowsEnglishWhenStringsFail(t *testing.T) {
	r := newRig(t)
	r.d.stringsErr = errors.New("HTTP 500")
	r.c.Refresh(context.Background())
	got := r.menuTitles()
	for _, want := range []string{"Open cockpit", "Sweep now", "Quit " + brand.Name} {
		if !slices.Contains(got, want) {
			t.Errorf("no %q in %q", want, got)
		}
	}
	s := ptBRStrings
	r.d.strings, r.d.stringsErr = &s, nil
	r.c.Refresh(context.Background())
	if got := r.menuTitles(); !slices.Contains(got, "Varrer agora") {
		t.Errorf("a later refresh kept English: %q", got)
	}
	r.d.stringsErr = errors.New("HTTP 500")
	r.c.Refresh(context.Background())
	if got := r.menuTitles(); !slices.Contains(got, "Varrer agora") {
		t.Errorf("a failed read dropped the served strings: %q", got)
	}
}

func TestTheDaemonDownMenuReadsTheCatalog(t *testing.T) {
	r := newRig(t)
	s := ptBRStrings
	r.d.strings = &s
	r.c.Refresh(context.Background())
	r.d.err = errors.New("connection refused")
	r.c.Refresh(context.Background())
	if got := r.menuTitles(); !slices.Contains(got, "Sair do "+brand.Name) {
		t.Errorf("%q", got)
	}
}

func TestNotifyNoteTitlesComeFromTheCatalog(t *testing.T) {
	r := newRig(t)
	s := ptBRStrings
	r.d.strings = &s
	r.c.Refresh(context.Background())
	r.d.failing = errors.New("no agent session")
	resp, _ := ResponseFor(questionTask(5).Attention.ID, "answer:text")
	resp.Text = "sim"
	r.c.Respond(context.Background(), resp)
	if n := r.notes.posted[len(r.notes.posted)-1]; n.Title != "Resposta não enviada" {
		t.Fatalf("%+v", n)
	}
}
