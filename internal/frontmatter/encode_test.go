package frontmatter

import (
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestValueReadsBackAsWritten(t *testing.T) {
	for _, v := range []any{"a \"b\" \\ c\td\x7f\x01é😀", "", []string{"acme/api", "it's"}, []string{}, true, int64(3), 0.5} {
		enc, err := Value(v)
		if err != nil {
			t.Fatalf("%#v: %v", v, err)
		}
		var doc map[string]any
		if err := toml.Unmarshal([]byte("v = "+enc), &doc); err != nil {
			t.Fatalf("%#v as %s: %v", v, enc, err)
		}
		back, _ := Value(doc["v"])
		if back != enc {
			t.Errorf("%#v: wrote %s, read back as %s", v, enc, back)
		}
	}
	if _, err := Value(map[string]any{"a": 1}); err == nil {
		t.Error("a table encoded as a value")
	}
}

func TestKeyQuotesOnlyWhatNeedsIt(t *testing.T) {
	for k, want := range map[string]string{"repos": "repos", "needs-me": "needs-me", "x.y": `'x.y'`, "": `''`} {
		if got := Key(k); got != want {
			t.Errorf("%q: %s, want %s", k, got, want)
		}
	}
}
