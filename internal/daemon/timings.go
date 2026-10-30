package daemon

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	timingsDefaultDays = 30
	timingsMaxDays     = 365
)

// handleTimings serves the forge's timings of the days before now, as its
// plugin reports them.
func (s *Server) handleTimings(w http.ResponseWriter, r *http.Request) {
	days := timingsDefaultDays
	if v := r.URL.Query().Get("since"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > timingsMaxDays {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("since is a number of days between 1 and %d", timingsMaxDays))
			return
		}
		days = n
	}
	s.writePluginView(w, r, unit.Unit{Provider: s.deps.Forge}, api.ViewTimings, api.TimingsQuery{Days: days})
}
