package daemon

import (
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

func closeAction(r *outboxRig, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.s.Handler().ServeHTTP(rec, apiRequest("POST", path, strings.NewReader(`{"source":"cockpit"}`)))
	return rec
}

var chatThread = unit.Unit{Provider: "chat", Key: "room/1700000000.1"}

// chatRig serves chatThread as a row of the chat plugin, in its view, with its items, over a daemon whose chat
// units close with no message.
func chatRig(t *testing.T, its ...src.Item) (*outboxRig, *[]string) {
	t.Helper()
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	r, applied := silentRig(t)
	r.s.deps.Units = unit.NewRegistry(pr.Codec{}, unit.OpaqueCodec{ID: "chat"})
	reports := snapshotReports{chatThread: {Unit: plugin.Unit{Provider: "chat", Key: chatThread.Key}, Rev: 1, At: at, Views: []string{"chat"},
		Situation: &src.Situation{Kind: "waiting", WaitingOn: "me", Since: at}, Card: &src.Card{Title: "Ana asks for the export"}, Items: its}}
	r.s.deps.RowProviders = []func(readmodel.RowDeps) readmodel.Provider{func(rd readmodel.RowDeps) readmodel.Provider {
		return readmodel.NewReported(readmodel.ReportedSource{RowDeps: rd, ID: "chat", Reports: reports})
	}}
	return r, applied
}

func TestCloseAppliesUnitClosedWithNoMessage(t *testing.T) {
	t.Parallel()
	r, applied := chatRig(t)
	rec := closeAction(r, "/api/units/chat/room%2F1700000000.1/unit.close")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !slices.Equal(*applied, []string{builtin.EffectUnitClosed}) || len(r.fx.calls) != 0 {
		t.Fatalf("applied %v, publisher calls %v", *applied, r.fx.calls)
	}
	if drafts, _ := r.o.Store.List(t.Context(), outbox.Filter{}); len(drafts) != 0 {
		t.Errorf("closing left drafts %v", drafts)
	}
}

func TestCloseRefusesAPullRequest(t *testing.T) {
	t.Parallel()
	r, applied := silentRig(t)
	rec := closeAction(r, "/api/units/github/acme%2Fapi%237/unit.close")
	if rec.Code != 409 || len(*applied) != 0 {
		t.Fatalf("%d %s, applied %v", rec.Code, rec.Body, *applied)
	}
}

func TestRowsOfAProviderThatClosesSilentlyOfferClose(t *testing.T) {
	t.Parallel()
	r, _ := silentRig(t)
	base, err := modelSettings{r.s}.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if base.Closes == nil || !base.Closes("chat") {
		t.Fatal("the rows' base says chat units do not close with no message")
	}
	r.o.Silent = nil
	if base.Closes("chat") {
		t.Error("units close with no message though no effect applies without a post")
	}
}

func TestCloseIsRefusedOnARowWithAnOpenItem(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		state string
		code  int
	}{{"open", 409}, {"resolved", 200}} {
		r, applied := chatRig(t, src.Item{ID: "i1", Kind: "request", Summary: "send the export", State: c.state})
		rec := closeAction(r, "/api/units/chat/room%2F1700000000.1/unit.close")
		if rec.Code != c.code || (c.code == 200) != (len(*applied) == 1) {
			t.Errorf("close with a %s item: %d %s, applied %v", c.state, rec.Code, rec.Body, *applied)
		}
	}
}
