package setup

import "testing"

func TestSessionValueNestsEachManagersVariable(t *testing.T) {
	for _, c := range []struct {
		vars []string
		want string
	}{
		{nil, ""},
		{[]string{"A"}, "${A}"},
		{[]string{"A", "B"}, "${A:-${B}}"},
		{[]string{"A", "B", "C"}, "${A:-${B:-${C}}}"},
	} {
		if got := SessionValue(c.vars); got != c.want {
			t.Errorf("SessionValue(%q) = %q, want %q", c.vars, got, c.want)
		}
	}
}
