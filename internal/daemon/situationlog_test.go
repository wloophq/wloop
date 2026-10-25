package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/github"
)

var (
	situationR7 = github.Ref{Owner: "acme", Repo: "api", Number: 7}
	situationR8 = github.Ref{Owner: "acme", Repo: "api", Number: 8}
)

func situationsOf(t *testing.T, s *Server, ref github.Ref) []string {
	t.Helper()
	evs, err := s.store().Events(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.Kind == dinv.KindSituation {
			out = append(out, e.Data)
		}
	}
	return out
}

type lineLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *lineLog) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *lineLog) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.Contains(line, sub) {
			n++
		}
	}
	return n
}

// keepOnce is one step of the read model's projector: a rebuild and its
// keeper.
func keepOnce(t *testing.T, s *Server) error {
	t.Helper()
	return s.readModel().Refresh(context.Background())
}

func TestSituationsAreRecordedOnChange(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	for range 2 {
		if err := keepOnce(t, s); err != nil {
			t.Fatal(err)
		}
	}
	if got := situationsOf(t, s, situationR7); !slices.Equal(got, []string{"review:me"}) {
		t.Fatalf("after two snapshots: %v", got)
	}
	if got := situationsOf(t, s, situationR8); got != nil {
		t.Fatalf("a merged pull request got situations %v", got)
	}
	if err := s.store().SetOverride(context.Background(), dinv.Override{PR: situationR7, MutedUntil: s.deps.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := keepOnce(t, s); err != nil {
		t.Fatal(err)
	}
	if got := situationsOf(t, s, situationR7); !slices.Equal(got, []string{"review:me", "muted:nobody"}) {
		t.Fatalf("after the mute: %v", got)
	}
}

func TestAFailedViewerRecordsNoSituation(t *testing.T) {
	t.Parallel()
	var down sync.Mutex
	failing := true
	s := newServerWith(t, func(d *Deps) {
		d.Viewer = func(context.Context) (string, []string, error) {
			down.Lock()
			defer down.Unlock()
			if failing {
				return "", nil, errors.New("github is down")
			}
			return "me", nil, nil
		}
	})
	for range 2 {
		if err := keepOnce(t, s); err == nil {
			t.Fatal("a rebuild without a viewer succeeded")
		}
	}
	if got := situationsOf(t, s, situationR7); got != nil {
		t.Fatalf("situations without a viewer: %v", got)
	}
	down.Lock()
	failing = false
	down.Unlock()
	s.viewer = viewerCache{}
	if err := keepOnce(t, s); err != nil {
		t.Fatal(err)
	}
	if got := situationsOf(t, s, situationR7); !slices.Equal(got, []string{"review:me"}) {
		t.Fatalf("after GitHub answers: %v", got)
	}
}

func TestWaitingUnreadSkipsSituation(t *testing.T) {
	t.Parallel()
	store := turnstest.NewStore()
	store.Put(turn.Book{Unit: pr.Ref(situationR7).Unit(), Turns: []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), ID: "r1", State: turn.Started, Session: "term_1",
		SentAt: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC), StartedAt: time.Date(2026, 10, 8, 9, 1, 0, 0, time.UTC)}}})
	reg := turnstest.NewRegistry()
	reg.Put(turn.Session{ID: "term_1", Pane: "tab-1:leaf-1", WorkspaceID: "ws-1"})
	sessions := turnstest.NewSessions()
	sessions.FailReads(errors.New("orca is not running"))
	s := newServerWith(t, func(d *Deps) {
		d.Turns = &turns.Service{Intents: turnstest.Intents(), Store: store, Registry: reg, Sessions: sessions, Clock: fixedClock(time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC))}
		d.Sessions = sessions
		d.Agents = snapshotOf(sessions)
	})
	if err := keepOnce(t, s); err != nil {
		t.Fatal(err)
	}
	if got := situationsOf(t, s, situationR7); got != nil {
		t.Fatalf("recorded %v while the agents could not be read", got)
	}
	sessions.FailReads(nil)
	sessions.SetAgents(turns.AgentObservation{WorkspaceID: "ws-1", Pane: "tab-1:leaf-1", State: turn.AgentWaiting})
	if err := keepOnce(t, s); err != nil {
		t.Fatal(err)
	}
	if got := situationsOf(t, s, situationR7); len(got) != 1 || !strings.HasPrefix(got[0], "loop_waiting:") {
		t.Fatalf("after the agents answer: %v", got)
	}
}
