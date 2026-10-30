package daemon

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTheSocketServesTheRuntimesProfilesAndThePortDoesNot(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	rec := httptest.NewRecorder()
	r.s.SocketHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "goroutine") {
		t.Fatalf("the socket's profiles: %d %.80s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	r.s.AuthHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if rec.Code == 200 || strings.Contains(rec.Body.String(), "goroutine") {
		t.Fatalf("the port served the profiles without the token: %d %.80s", rec.Code, rec.Body.String())
	}
}

func TestASlowRequestIsLoggedWithItsPathAndDuration(t *testing.T) {
	t.Parallel()
	var logged []string
	r := newTasksRig(t, func(d *Deps) {
		now := tasksNow
		d.Now = func() time.Time { now = now.Add(3 * time.Second); return now }
		d.Log = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	})
	if code := get(t, r.s, "/api/health", nil); code != 200 {
		t.Fatalf("health: %d", code)
	}
	found := false
	for _, l := range logged {
		if strings.Contains(l, "slow request") && strings.Contains(l, "/api/health") && strings.Contains(l, "3s") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no slow request logged: %q", logged)
	}
}
