package daemon

import (
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

func TestOperationsAreRoutedOnce(t *testing.T) {
	t.Parallel()
	s := newServerWith(t, func(*Deps) {})
	routed := map[string]int{}
	for _, rt := range s.routes() {
		routed[rt.pattern]++
	}
	for _, op := range api.Operations {
		if n := routed[op.Pattern()]; n != 1 {
			t.Errorf("%s is routed %d times", op.Pattern(), n)
		}
		delete(routed, op.Pattern())
	}
	for p := range routed {
		t.Errorf("%s is routed but not in api.Operations", p)
	}
}
