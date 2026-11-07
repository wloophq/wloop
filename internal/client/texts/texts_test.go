package texts

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

func TestEnglishReadsEachKeyAsItsText(t *testing.T) {
	c := English()
	cases := []struct{ got, want string }{
		{c.T("Sweep now", nil), "Sweep now"},
		{c.T("Quit {name}", map[string]any{"name": "Loop"}), "Quit Loop"},
		{c.T("{n} of {total}", map[string]any{"n": 2}), "2 of {total}"},
		{c.TN("{n} drafts", 3, nil), "3 drafts"},
		{c.TN("{n} drafts in {where}", 2, map[string]any{"where": "Outbox"}), "2 drafts in Outbox"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestTNPicksOneForOneElseOther(t *testing.T) {
	c := &Catalog{strings: map[string]api.Text{"{n} drafts": {"one": "{n} draft", "other": "{n} drafts"}, "Sync": {"other": "Sync"}}}
	for n, want := range map[int]string{0: "0 drafts", 1: "1 draft", 2: "2 drafts"} {
		if got := c.TN("{n} drafts", n, nil); got != want {
			t.Errorf("TN(%d) = %q, want %q", n, got, want)
		}
	}
	if got := c.TN("Sync", 1, nil); got != "Sync" {
		t.Errorf("a text without a one form: %q", got)
	}
}

func TestCatalogTAndTN(t *testing.T) {
	c := New(api.Strings{Locale: "pt-BR", Strings: map[string]api.Text{"Sweep now": {"other": "Varrer agora"}, "{n} drafts": {"one": "{n} rascunho", "other": "{n} rascunhos"}},
		Formats: map[string]string{"time": "15:04", "date": "02/01", "group": "."}})
	at := time.Date(2026, 11, 7, 21, 5, 0, 0, time.UTC)
	for _, tc := range []struct{ got, want string }{
		{c.T("Sweep now", nil), "Varrer agora"},
		{c.T("Unknown key", nil), "Unknown key"},
		{c.TN("{n} drafts", 0, nil), "0 rascunho"},
		{c.TN("{n} drafts", 1500, nil), "1.500 rascunhos"},
		{c.Time(at, "time"), "21:05"},
		{c.Time(at, "date"), "07/11"},
		{c.Time(at, "datetime"), "Nov 7 9:05 PM"},
		{c.Number(-1234567), "-1.234.567"},
		{c.Locale(), "pt-BR"},
		{English().Time(at, "time"), "9:05 PM"},
		{English().Number(1234), "1,234"},
		{English().Locale(), "en"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}
