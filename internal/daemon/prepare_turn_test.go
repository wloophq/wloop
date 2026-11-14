package daemon

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

func TestPrepareTurnFillsWhatTheActionsPrepareFills(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	seedWaitingOnAna(t, s)
	got, err := s.PrepareTurn(context.Background(), string(dintent.Nudge), r9.Unit(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	situation, _ := got["situation"].(string)
	if who, _ := got["who"].([]string); !slices.Equal(who, []string{"@ana"}) || !strings.HasPrefix(situation, "Waiting on: @ana since ") {
		t.Fatalf("inputs %#v", got)
	}
}

func TestPrepareTurnRefusalIsUnfit(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	seedWaitingOnAna(t, s)
	sentNudge(t, s, "t1", "@ana")
	_, err := s.PrepareTurn(context.Background(), string(dintent.Nudge), r9.Unit(), "", nil)
	if !errors.Is(err, src.ErrUnfit) || !strings.Contains(err.Error(), "already nudged") {
		t.Fatalf("a second nudge of the same wait: %v", err)
	}
}

func TestPrepareTurnOfAnIntentWithoutOneKeepsItsInputs(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	in := map[string]any{"self_review": true}
	got, err := s.PrepareTurn(context.Background(), string(dintent.Address), r7.Unit(), "", in)
	if err != nil || !maps.Equal(got, in) {
		t.Fatalf("%v %v", got, err)
	}
}

func TestPrepareTurnOnADisabledPluginIsUnfit(t *testing.T) {
	t.Parallel()
	s, _ := newActServer(t, func(p unit.ProviderID) bool { return p != unit.GitHub })
	seedWaitingOnAna(t, s)
	if _, err := s.PrepareTurn(context.Background(), string(dintent.Nudge), r9.Unit(), "", nil); !errors.Is(err, src.ErrUnfit) {
		t.Fatalf("the prepare of a disabled plugin: %v", err)
	}
}

func TestAHeadlessActionAnswersItsStartsRefusalAndOutage(t *testing.T) {
	t.Parallel()
	s, h := newThreadActServer(t)
	h.err = kindError{turns.ErrRefused, fmt.Errorf("%w: nobody to ask", src.ErrUnfit)}
	if rec := post(t, s, threadAct, `{"items":["i1"]}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "nobody to ask") || strings.Contains(rec.Body.String(), src.ErrUnfit.Error()) {
		t.Fatalf("a refused start: %d %s", rec.Code, rec.Body)
	}
	h.err = kindError{turns.ErrTransient, fmt.Errorf("%w: the plugin is restarting", src.ErrUnavailable)}
	if rec := post(t, s, threadAct, `{"items":["i1"]}`); rec.Code != 503 || !strings.Contains(rec.Body.String(), "the plugin is restarting") {
		t.Fatalf("a start out of reach: %d %s", rec.Code, rec.Body)
	}
}

// kindError is err read as kind too, as the start's prepare answers it.
type kindError struct{ kind, err error }

func (e kindError) Error() string   { return e.err.Error() }
func (e kindError) Unwrap() []error { return []error{e.kind, e.err} }

func TestAPrepareRefusalAnswersTheStatusOfItsDaemonError(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	seedWaitingOnAna(t, s)
	sentNudge(t, s, "t1", "@ana")
	_, err := s.PrepareTurn(context.Background(), string(dintent.Nudge), r9.Unit(), "", nil)
	rec := httptest.NewRecorder()
	writeActionError(rec, err)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "already nudged") || strings.Contains(rec.Body.String(), src.ErrUnfit.Error()) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	off, _ := newActServer(t, func(p unit.ProviderID) bool { return p != unit.GitHub })
	seedWaitingOnAna(t, off)
	_, err = off.PrepareTurn(context.Background(), string(dintent.Nudge), r9.Unit(), "", nil)
	rec = httptest.NewRecorder()
	writeActionError(rec, err)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a disabled plugin's prepare: %d %s", rec.Code, rec.Body)
	}
}
