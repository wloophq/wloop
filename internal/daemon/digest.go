package daemon

import (
	"fmt"
	"net/http"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
)

// handleDigest serves the daily digest of the query parameter day: the counters, and the tasks that moved
// into Next or Waiting since the previous digest, at notifications.digest_at local time (midnight when off).
func (s *Server) handleDigest(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	day := r.URL.Query().Get("day")
	if day == "" {
		day = s.deps.Now().Local().Format(time.DateOnly)
	}
	end, err := digestEnd(day, s.deps.Cfg.Notifications.DigestAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	start := end.AddDate(0, 0, -1)
	ctx := r.Context()
	d := api.Digest{Day: day, Title: "Daily digest"}
	for _, v := range []task.View{task.ViewNext, task.ViewWaiting} {
		es, err := g.Tasks(ctx, task.Query{View: v})
		if err != nil {
			writeTasksError(w, err)
			return
		}
		for _, e := range es {
			if e.Updated.After(start) && !e.Updated.After(end) && s.unitsEnabled(e.Units) {
				d.Tasks = append(d.Tasks, string(e.ID))
			}
		}
	}
	c, err := g.Counts(ctx, "")
	if err != nil {
		writeTasksError(w, err)
		return
	}
	d.Counts = wireCounters(c)
	d.Body = fmt.Sprintf("%d new in Next or Waiting · %d in Inbox · %d in Outbox · %d next · %d waiting", len(d.Tasks),
		d.Counts.Views[string(task.ViewInbox)], d.Counts.Views[string(task.ViewOutbox)], d.Counts.Views[string(task.ViewNext)], d.Counts.Views[string(task.ViewWaiting)])
	writeJSON(w, d)
}

// digestEnd is day's digest instant: at, a local "HH:MM", that day; midnight when at is "".
func digestEnd(day, at string) (time.Time, error) {
	d, err := time.ParseInLocation(time.DateOnly, day, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("day %q is not YYYY-MM-DD", day)
	}
	if at == "" {
		return d, nil
	}
	hm, err := time.Parse("15:04", at)
	if err != nil {
		return time.Time{}, fmt.Errorf("notifications.digest_at %q is not HH:MM", at)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), hm.Hour(), hm.Minute(), 0, 0, time.Local), nil
}
