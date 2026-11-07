package texts

import (
	"testing"

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

func TestCatalogTAndTN(t *testing.T) { t.Skip("D11 Catalog T and TN read the served strings") }
