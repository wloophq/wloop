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
		name  string
		sess  Session
		turns []Turn
		want  bool
	}{
		{"the loop never typed into it", Session{ID: id}, nil, true},
		{"the user typed after the loop", Session{ID: id, TypedAt: at(1), UserPromptAt: at(2)}, nil, true},
		{"the loop typed last", Session{ID: id, TypedAt: at(2), UserPromptAt: at(1)}, nil, false},
		{"the loop typed and the user never did", Session{ID: id, TypedAt: at(1)}, nil, false},
		{"a sent turn runs in it", Session{ID: id, TypedAt: at(1), UserPromptAt: at(2)}, []Turn{{Seq: 1, State: Sent, SentTo: id}}, false},
		{"a started turn runs in it", Session{ID: id}, []Turn{{Seq: 1, State: Started, Session: id}}, false},
		{"its turn asks the user", Session{ID: id, TypedAt: at(1), UserPromptAt: at(2)}, []Turn{{Seq: 1, State: Asked, Session: id}}, true},
		{"its turn ended", Session{ID: id}, []Turn{{Seq: 1, State: Done, Session: id}}, true},
		{"another session's turn runs", Session{ID: id}, []Turn{{Seq: 1, State: Started, Session: "s-2"}}, true},
		{"a headless turn runs", Session{ID: id}, []Turn{{Seq: 1, State: Started, Session: id, Runner: RunnerHeadless}}, true},
	} {
		if got := c.sess.UserDriven(c.turns); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
