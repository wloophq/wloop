package daemon

import (
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func closeAction(r *outboxRig, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.s.Handler().ServeHTTP(rec, apiRequest("POST", path, strings.NewReader(`{"source":"cockpit"}`)))
	return rec
}

func TestCloseAppliesUnitClosedWithNoMessage(t *testing.T) {
	t.Parallel()
	r, applied := silentRig(t)
	r.s.deps.Units = unit.NewRegistry(pr.Codec{}, unit.OpaqueCodec{ID: "chat"})
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
