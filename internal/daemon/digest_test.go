package daemon

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func digestRig(t *testing.T) (*entriesRig, time.Time) {
	t.Helper()
	r := newEntriesRig(t, func(d *Deps) {
		d.Cfg.Notifications.DigestAt = "09:00"
		d.PluginEnabled = func(p unit.ProviderID) bool { return p != "slack" }
	})
	return r, time.Date(2026, 10, 8, 9, 0, 0, 0, time.Local)
}

func at(e task.Entry, t time.Time) task.Entry {
	e.Created, e.Updated = t.Add(-48*time.Hour), t
	return e
}

func TestDigestContents(t *testing.T) {
	t.Parallel()
	r, end := digestRig(t)
	r.put(t,
		at(task.Entry{ID: "e-next", State: task.Next}, end.Add(-time.Hour)),
		at(task.Entry{ID: "e-wait", Kind: task.KindWaiting, State: task.Waiting, Waiting: &task.WaitingOn{Since: end}}, end.Add(-20*time.Hour)),
		at(task.Entry{ID: "e-old", State: task.Next}, end.Add(-25*time.Hour)),
		at(task.Entry{ID: "e-later", State: task.Next}, end.Add(time.Minute)),
		at(task.Entry{ID: "e-in", State: task.Inbox}, end.Add(-time.Hour)),
		at(task.Entry{ID: "e-off", State: task.Next, Units: []unit.Unit{{Provider: "slack", Key: "T000/C000/1.2"}}}, end.Add(-time.Hour)),
	)
	var d api.Digest
	if code := get(t, r.s, api.DigestPath+"?day=2026-10-08", &d); code != 200 {
		t.Fatalf("%d", code)
	}
	if d.Day != "2026-10-08" || !slices.Equal(d.Entries, []string{"e-next", "e-wait"}) || d.Title == "" ||
		d.Counts.Views["inbox"] != 1 || d.Counts.Views["next"] != 3 || !strings.Contains(d.Body, "2 new") {
		t.Fatalf("%+v", d)
	}
	if code := get(t, r.s, api.DigestPath+"?day=08/10/2026", &d); code != 400 {
		t.Fatalf("a bad day: %d", code)
	}
}

func TestEmptyDigest(t *testing.T) {
	t.Parallel()
	r, end := digestRig(t)
	r.put(t, at(task.Entry{ID: "e-old", State: task.Next}, end.Add(-30*time.Hour)))
	raw := getRaw(t, r.s, api.DigestPath+"?day=2026-10-08")
	if !strings.Contains(raw, `"entries":null`) {
		t.Fatalf("%s", raw)
	}
}
