package usage

import "testing"

func TestPlusAddsEveryField(t *testing.T) {
	a := Tokens{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}
	b := Tokens{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40}
	got := a.Plus(b)
	want := Tokens{Input: 11, Output: 22, CacheRead: 33, CacheWrite: 44}
	if got != want {
		t.Fatalf("Plus = %+v, want %+v", got, want)
	}
}
