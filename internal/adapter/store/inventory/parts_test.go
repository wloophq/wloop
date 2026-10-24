package inventory

import (
	"strings"
	"testing"
	"testing/quick"
)

func TestSplitJoinLossless(t *testing.T) {
	for _, text := range []string{
		"", "plain", "trailing\n", "\n\n", "a\r\n# b\r\nc", "# first\n## second\n#### deep\n", "a\n#nospace\n# x", "a\n\n# b\n",
	} {
		if got := joinParts(splitParts(text)); got != text {
			t.Errorf("%q -> %q", text, got)
		}
	}
	if got := splitParts("a\n# b\n## c\n### d\n#### e"); len(got) != 4 {
		t.Errorf("headings of level 1 to 3 split, level 4 does not: %q", got)
	}
	fuzz := func(pieces []uint8) bool {
		alphabet := []string{"a", "\n", "#", " ", "\r", "##", "x y"}
		var b strings.Builder
		for _, p := range pieces {
			b.WriteString(alphabet[int(p)%len(alphabet)])
		}
		return joinParts(splitParts(b.String())) == b.String()
	}
	if err := quick.Check(fuzz, &quick.Config{MaxCount: 2000}); err != nil {
		t.Error(err)
	}
}

func TestDeflateRoundTrip(t *testing.T) {
	body, err := deflatePart(strings.Repeat("guide ", 100))
	if err != nil || len(body) >= 600 {
		t.Fatalf("%d %v", len(body), err)
	}
	if got, err := inflatePart(body); err != nil || got != strings.Repeat("guide ", 100) {
		t.Fatalf("%v", err)
	}
}
