package turns_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// orphanEnv is c's session with its process orphaned, proven durable by a
// renderer restart it outlived when prove is set.
func orphanEnv(t *testing.T, c cleanupCase, prove bool) closeEnv {
	t.Helper()
	e := newCloseEnv(t, c)
	e.sess.Orphan(c.sess.ID)
	readings := []string{"e1"}
	if prove {
		readings = []string{"e1", "e1", "e2", "e2"}
	}
	for i, epoch := range readings {
		e.sess.SetReading(epoch, t0.Add(-10*time.Minute+time.Duration(i)*e.s.FastJob))
		if err := e.s.Census(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if e.s.OrphanDurable(c.sess.ID) != prove {
		t.Fatalf("durable %v, want %v", !prove, prove)
	}
	return e
}

func TestCloseOrphanEndsADurableOrphansProcessOnly(t *testing.T) {
	e := orphanEnv(t, cleanupCase{sess: quietSession("term_1")}, true)
	ctx := context.Background()
	if why, err := e.s.CloseOrphanRefusal(ctx, "term_1"); why != "" || err != nil {
		t.Fatalf("refusal %q %v", why, err)
	}
	if err := e.s.CloseOrphan(ctx, "term_1"); err != nil {
		t.Fatal(err)
	}
	if closed := e.sess.Closed(); len(closed) != 1 || closed[0] != "term_1" {
		t.Fatalf("closed %v", closed)
	}
	if got, _, _ := e.reg.Session(ctx, "term_1"); got.State() != turn.SessionActive {
		t.Fatalf("session %+v: closing the orphan's process recorded a state; the census does", got)
	}
}

func TestCloseOrphanRefusesWhatASessionCloseRefuses(t *testing.T) {
	started := []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), ID: "r1", Trigger: turn.Automated, State: turn.Started,
		Session: "term_1", SentTo: "term_1", SentAt: t0.Add(-time.Minute), StartedAt: t0.Add(-time.Minute)}}
	typed := quietSession("term_1")
	typed.UserPromptAt = t0.Add(-time.Hour)
	for _, c := range []struct {
		name  string
		c     cleanupCase
		prove bool
		want  string
	}{
		{"a started turn on a working agent", cleanupCase{sess: quietSession("term_1"), turns: started, state: turn.AgentWorking}, true, "its turn is started"},
		{"a working agent", cleanupCase{sess: quietSession("term_1"), state: turn.AgentWorking}, true, "its agent is working"},
		{"an agent waiting for permission", cleanupCase{sess: quietSession("term_1"), state: turn.AgentWaiting}, true, "its agent is waiting for permission"},
		{"its workspace focused", cleanupCase{sess: quietSession("term_1"), userActive: true}, true, "its workspace is the one open in the app"},
		{"you typed after the loop", cleanupCase{sess: typed}, true, "you typed in it after"},
		{"an orphan not proven durable", cleanupCase{sess: quietSession("term_1")}, false, turns.RefusalOrphanUnproven},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := orphanEnv(t, c.c, c.prove)
			ctx := context.Background()
			why, err := e.s.CloseOrphanRefusal(ctx, "term_1")
			if err != nil || why == "" || !strings.Contains(why, c.want) {
				t.Fatalf("refusal %q %v, want %q", why, err, c.want)
			}
			var refusal turns.Refusal
			if err := e.s.CloseOrphan(ctx, "term_1"); !errors.As(err, &refusal) || refusal.Reason != why {
				t.Fatalf("CloseOrphan = %v, want the refusal %q", err, why)
			}
			if closed := e.sess.Closed(); len(closed) != 0 {
				t.Fatalf("closed %v", closed)
			}
		})
	}
}

// orphanedEnv is c's session whose process two readings FastJob apart list orphaned.
func orphanedEnv(t *testing.T, c cleanupCase) closeEnv {
	t.Helper()
	e := newCloseEnv(t, c)
	e.sess.Orphan(c.sess.ID)
	for i := range 2 {
		e.sess.SetReading("", t0.Add(-10*time.Minute+time.Duration(i)*e.s.FastJob))
		if err := e.s.Census(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if !e.s.ProcessOrphaned(c.sess.ID) {
		t.Fatal("not orphaned")
	}
	return e
}

func TestMarkDormantRetiresAQuietOrphanWithoutEndingItsProcessAndAWindowWakesIt(t *testing.T) {
	ctx := context.Background()
	e := orphanedEnv(t, cleanupCase{sess: quietSession("term_1")})
	if why, err := e.s.DormantRefusal(ctx, "term_1"); why != "" || err != nil {
		t.Fatalf("refusal %q %v", why, err)
	}
	if err := e.s.MarkDormant(ctx, "term_1"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := e.reg.Session(ctx, "term_1"); got.State() != turn.SessionDormant {
		t.Fatalf("session %+v, want dormant", got)
	}
	if closed := e.sess.Closed(); len(closed) != 0 {
		t.Fatalf("retiring the session ended %v", closed)
	}
	e.sess.Unorphan("term_1")
	if err := e.s.Census(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := e.reg.Session(ctx, "term_1"); got.State() != turn.SessionActive {
		t.Fatalf("session %+v, want woken once a window shows its process again", got)
	}
}

func TestMarkDormantRefusesAnOrphanWhatASessionCloseRefusesWithNoPane(t *testing.T) {
	started := []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), ID: "r1", Trigger: turn.Automated, State: turn.Started,
		Session: "term_1", SentTo: "term_1", SentAt: t0.Add(-time.Minute), StartedAt: t0.Add(-time.Minute)}}
	typed := quietSession("term_1")
	typed.UserPromptAt = t0.Add(-time.Hour)
	for _, c := range []struct {
		name string
		c    cleanupCase
		want string
	}{
		{"a started turn", cleanupCase{sess: quietSession("term_1"), turns: started, state: turn.AgentWorking}, "its turn is started"},
		{"a working agent", cleanupCase{sess: quietSession("term_1"), state: turn.AgentWorking}, "its agent is working"},
		{"you typed after the loop", cleanupCase{sess: typed}, "you typed in it after"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := orphanedEnv(t, c.c)
			ctx := context.Background()
			if err := e.s.MarkDormant(ctx, "term_1"); !refusal(err, c.want) {
				t.Fatalf("MarkDormant = %v, want %q", err, c.want)
			}
			if got, _, _ := e.reg.Session(ctx, "term_1"); got.State() != turn.SessionActive || len(e.sess.Closed()) != 0 {
				t.Fatalf("session %+v, closed %v", got, e.sess.Closed())
			}
		})
	}
}

func TestCloseOrphanRefusesAnOrphanAFreshCensusNoLongerShows(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name  string
		after func(e closeEnv)
	}{
		{"reattached", func(e closeEnv) { e.sess.Unorphan("term_1") }},
		{"another epoch", func(e closeEnv) { e.sess.SetReading("e3", time.Time{}) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := orphanEnv(t, cleanupCase{sess: quietSession("term_1")}, true)
			c.after(e)
			var refusal turns.Refusal
			if err := e.s.CloseOrphan(ctx, "term_1"); !errors.As(err, &refusal) {
				t.Fatalf("CloseOrphan = %v, want a refusal", err)
			}
			if closed := e.sess.Closed(); len(closed) != 0 {
				t.Fatalf("closed %v", closed)
			}
		})
	}
}

func TestCloseOrphanOfASessionWithNoPaneReadsItsWorkspacesAgents(t *testing.T) {
	nopane := quietSession("term_1")
	nopane.Pane = ""
	e := orphanEnv(t, cleanupCase{sess: nopane, state: turn.AgentWorking}, true)
	why, err := e.s.CloseOrphanRefusal(context.Background(), "term_1")
	if err != nil || !strings.Contains(why, "busy") {
		t.Fatalf("refusal %q %v, want a busy agent of its workspace", why, err)
	}
}

func TestCloseOrphanOfASessionGoneClosedOrDormantIsGone(t *testing.T) {
	ctx := context.Background()
	closed := quietSession("term_1")
	closed.ClosedAt = t0
	dormant := quietSession("term_2")
	dormant.Handle = ""
	s, store, reg, sess, _ := newService(t)
	cleanupCase{sess: closed}.put(reg, store, sess)
	cleanupCase{sess: dormant}.put(reg, store, sess)
	for _, id := range []string{"term_1", "term_2", "term_absent"} {
		why, err := s.CloseOrphanRefusal(ctx, id)
		if err != nil || !turns.CloseOrphanGone(id, why) {
			t.Fatalf("%s: refusal %q %v, want one that holds for good", id, why, err)
		}
		var gone turns.Gone
		if err := s.CloseOrphan(ctx, id); !errors.As(err, &gone) {
			t.Fatalf("%s: CloseOrphan = %v", id, err)
		}
	}
	if len(sess.Closed()) != 0 {
		t.Fatalf("closed %v", sess.Closed())
	}
}
