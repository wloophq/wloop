package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

func windowResult(t *testing.T, s *Server, body string) api.WindowResult {
	t.Helper()
	rec := post(t, s, api.WindowPath, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST %s %s: %d %s", api.WindowPath, body, rec.Code, rec.Body)
	}
	var res api.WindowResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestWindowRequestReachesOnlyTheWindowClient(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	win := s.hub.subscribe(api.WindowClient)
	app := s.hub.subscribe(notifierClient)
	t.Cleanup(func() { s.hub.unsubscribe(win); s.hub.unsubscribe(app) })
	if res := windowResult(t, s, `{"action":"open","route":"task=t-1"}`); !res.Delivered {
		t.Fatal("not delivered with a window client listening")
	}
	if c, ok := win.take(); !ok || c.Window == nil || *c.Window != (api.WindowRequest{Action: api.WindowOpen, Route: "task=t-1"}) {
		t.Fatalf("the window client took %+v (%v)", c, ok)
	}
	if c, ok := app.take(); ok {
		t.Fatalf("another client took %+v", c)
	}
}

func TestWindowRequestIsNotDeliveredWithoutAWindowClient(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	app := s.hub.subscribe(notifierClient)
	t.Cleanup(func() { s.hub.unsubscribe(app) })
	if res := windowResult(t, s, `{"action":"panel"}`); res.Delivered {
		t.Fatal("delivered with no window client")
	}
}

func TestWindowRequestRefusesAnUnknownAction(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	rec := post(t, s, api.WindowPath, `{"action":"maximize"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d %s, want 400", rec.Code, rec.Body)
	}
	for _, a := range []string{api.WindowOpen, api.WindowPanel, api.WindowHidePanel} {
		if !strings.Contains(rec.Body.String(), a) {
			t.Errorf("the refusal %s does not list %q", rec.Body, a)
		}
	}
}

func TestWindowRequestLeavesTheRowPositionAlone(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	win := s.hub.subscribe(api.WindowClient)
	t.Cleanup(func() { s.hub.unsubscribe(win) })
	pos := api.Since{Epoch: "e1", Rev: 9}
	s.hub.moveTo(pos)
	windowResult(t, s, `{"action":"hide-panel"}`)
	if got := s.hub.position(); got != pos {
		t.Fatalf("position moved to %v", got)
	}
	if c, _ := win.take(); c.Epoch != "" {
		t.Fatalf("the window change carries epoch %q; a client would resume from it", c.Epoch)
	}
}
