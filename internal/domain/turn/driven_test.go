package turn

import (
	"testing"
	"time"
)

func TestUserDriven(t *testing.T) {
	t.Parallel()
	at := func(m int) time.Time { return time.Date(2026, 3, 2, 10, m, 0, 0, time.UTC) }
	const id = "s-1"
	for _, c := range []struct {
		name string
		sess Session
		want bool
	}{
		{"the loop never typed into it", Session{ID: id}, true},
		{"the user prompted it after the loop's last typed line, a loop turn still running in it", Session{ID: id, TypedAt: at(1), UserPromptAt: at(2)}, true},
		{"the loop typed last", Session{ID: id, TypedAt: at(2), UserPromptAt: at(1)}, false},
		{"the loop typed and the user never did", Session{ID: id, TypedAt: at(1)}, false},
	} {
		if got := c.sess.UserDriven(); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
