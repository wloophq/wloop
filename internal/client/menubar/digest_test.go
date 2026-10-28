package menubar

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

var digestDay = time.Date(2026, 10, 14, 8, 59, 0, 0, time.Local)

func digestRig(t *testing.T, path, at string, start time.Time) (*rig, *fakeClock) {
	t.Helper()
	r, k := clockedRig(t)
	k.now = start
	r.c.cfg.SentPath, r.c.cfg.DigestAt = path, at
	r.c.loadSent()
	day := start.Format(time.DateOnly)
	r.d.digests[day] = api.Digest{Day: day, Title: "Daily digest", Body: "2 new in Next or Waiting · 1 in Inbox", Tasks: []string{"e-1", "e-2"}}
	return r, k
}

func digests(n *fakeNotifier) []Notification {
	return slices.DeleteFunc(n.snapshot(), func(p Notification) bool { return p.Category != api.CategoryDigest })
}

func TestDigestOnceADay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notified.json")
	r, k := digestRig(t, path, "09:00", digestDay)
	stop := run(t, r)
	waitFor(t, func() bool { return slices.Contains(k.waited(), time.Minute) })
	if len(digests(r.notes)) != 0 {
		t.Fatalf("posted before 09:00: %+v", digests(r.notes))
	}
	k.advance(time.Minute)
	waitFor(t, func() bool { return len(digests(r.notes)) == 1 })
	n := digests(r.notes)[0]
	if n.ID != "DIGEST|-|2026-10-14" || n.Title != "Daily digest" || n.Body != "2 new in Next or Waiting · 1 in Inbox" {
		t.Fatalf("posted %+v", n)
	}
	waitFor(t, func() bool { return slices.Contains(k.waited(), 24*time.Hour) })
	stop()

	again, k2 := digestRig(t, path, "09:00", digestDay.Add(2*time.Hour))
	stop = run(t, again)
	waitFor(t, func() bool { return slices.Contains(k2.waited(), 22*time.Hour+time.Minute) })
	stop()
	if len(digests(again.notes)) != 0 || len(again.d.digestDays) != 0 {
		t.Fatalf("a restart the same day posted %+v, asked %q", digests(again.notes), again.d.digestDays)
	}
	r.c.Respond(context.Background(), Response{ID: n.ID, Action: ActionOpen})
	if o := r.openedURLs(); !slices.Equal(o, []string{"cockpit#view=next"}) {
		t.Fatalf("the digest opened %v", o)
	}
}

func TestDigestOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, k := digestRig(t, "", "", digestDay.Add(time.Hour))
		r.d.events = make(chan api.Change)
		stop := run(t, r)
		synctest.Wait()
		k.advance(48 * time.Hour)
		synctest.Wait()
		stop()
		if len(digests(r.notes)) != 0 || len(r.d.digestDays) != 0 || len(k.waited()) != 0 {
			t.Fatalf("digest off posted %+v, asked %q, waited %v", digests(r.notes), r.d.digestDays, k.waited())
		}
	})
}

func TestEmptyDigestIsSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notified.json")
	r, k := digestRig(t, path, "09:00", digestDay.Add(time.Minute))
	day := digestDay.Format(time.DateOnly)
	r.d.digests[day] = api.Digest{Day: day, Title: "Daily digest", Body: "0 new in Next or Waiting · 0 in Inbox"}
	stop := run(t, r)
	waitFor(t, func() bool { return slices.Contains(k.waited(), 24*time.Hour) })
	stop()
	if len(digests(r.notes)) != 0 || !slices.Equal(r.d.digestDays, []string{day}) {
		t.Fatalf("posted %+v, asked %q", digests(r.notes), r.d.digestDays)
	}
}
