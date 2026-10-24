package link

import "testing"

func TestKindValid(t *testing.T) {
	for _, k := range []Kind{Mention, Review, Merge} {
		if !k.Valid() {
			t.Errorf("%q is not valid", k)
		}
	}
	for _, k := range []Kind{"", "Review", "mentions", "close"} {
		if k.Valid() {
			t.Errorf("%q is valid", k)
		}
	}
}
