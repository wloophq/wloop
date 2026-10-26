package gtd

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTheEntryCodecTakesAnEntryID(t *testing.T) {
	t.Parallel()
	r := unit.NewRegistry(Codec{})
	u, err := r.Parse("entry", "e-0a1b2c3d4e5f6a7b")
	if err != nil || u != UnitOf("e-0a1b2c3d4e5f6a7b") {
		t.Fatalf("%v %v", u, err)
	}
	for _, bad := range []string{"", " ", "e 1", "e\n1"} {
		if _, err := r.Parse("entry", bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
