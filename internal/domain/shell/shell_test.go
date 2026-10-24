package shell

import "testing"

func TestQuoteAndWord(t *testing.T) {
	for _, c := range []struct{ in, quote, word string }{
		{"/a/b", "'/a/b'", "/a/b"},
		{"", "''", "''"},
		{"it's", `'it'\''s'`, `'it'\''s'`},
		{"/Application Support/x", "'/Application Support/x'", "'/Application Support/x'"},
		{"~/x", "'~/x'", "'~/x'"},
	} {
		if got := Quote(c.in); got != c.quote {
			t.Errorf("Quote(%q) = %q, want %q", c.in, got, c.quote)
		}
		if got := Word(c.in); got != c.word {
			t.Errorf("Word(%q) = %q, want %q", c.in, got, c.word)
		}
	}
}
