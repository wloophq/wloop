package daemon

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// handleDigest serves the daily digest of the query parameter day: the counters, and the tasks of the views the
// profile's badge counts that moved since the previous digest, at notifications.digest_at local time (midnight
// when off).
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
	d := api.Digest{Day: day, Title: s.notice(builtin.NoticeDigestTitle, nil)}
	def := s.loaded().Views
	var counts []string
	c, err := g.Counts(ctx, "")
	if err != nil {
		writeTasksError(w, err)
		return
	}
	d.Counts = wireCounters(c)
	for _, id := range def.Badge {
		v, _ := def.View(id)
		counts = append(counts, s.notice(builtin.NoticeDigestView, map[string]any{"View": v.Label, "Count": d.Counts.Views[id]}))
		es, err := g.Tasks(ctx, task.Query{View: task.View(id)})
		if err != nil {
			writeTasksError(w, err)
			return
		}
		for _, e := range es {
			if e.Updated.After(start) && !e.Updated.After(end) && s.unitsEnabled(e.Units) && !slices.Contains(d.Tasks, string(e.ID)) {
				d.Tasks = append(d.Tasks, string(e.ID))
			}
		}
	}
	d.Body = strings.Join(append([]string{s.notice(builtin.NoticeDigestNew, map[string]any{"Count": len(d.Tasks)})}, counts...), " · ")
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
