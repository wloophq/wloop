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

func TestCutReadsTheWordWordWrote(t *testing.T) {
	for _, in := range []string{"/a/b", "", "it's", "/Application Support/x", "~/x", "a'b'c"} {
		for _, w := range []string{Word(in), Quote(in)} {
			got, rest, ok := Cut(w + " hook stop || true")
			if !ok || got != in || rest != " hook stop || true" {
				t.Errorf("Cut(%q) = %q, %q, %v", w, got, rest, ok)
			}
		}
	}
}

func TestCutRefusesAWordItDidNotWrite(t *testing.T) {
	for _, in := range []string{"'unclosed", `"/a b"`, `/a\ b`, "'a'b"} {
		if got, rest, ok := Cut(in); ok {
			t.Errorf("Cut(%q) = %q, %q, true", in, got, rest)
		}
	}
}
