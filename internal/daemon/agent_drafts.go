package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// AgentOutbox is the outbox's API for an agent's calls on drafts.
type AgentOutbox interface {
	AgentRead(ctx context.Context, c outbox.Caller, id draft.ID) (draft.Draft, []draft.Revision, error)
	AgentAmend(ctx context.Context, c outbox.Caller, id draft.ID, a outbox.Amendment) (draft.Draft, error)
	AgentWithdraw(ctx context.Context, c outbox.Caller, id draft.ID, rev int64, reason string) (draft.Draft, error)
}

var _ AgentOutbox = (*outbox.Service)(nil)

// AgentProposer proposes session's draft p on u through u's plugin, the host path its draft tool takes.
type AgentProposer func(ctx context.Context, session string, u unit.Unit, p api.AgentDraftProposal) (draft.ID, error)

// AgentDrafts are an agent session's calls on drafts, which the agent route and the MCP draft tools
// share. The session is the caller's authenticated one, never one a request names.
type AgentDrafts struct {
	Outbox AgentOutbox
	// Unit is the unit of session's turns; ok is false for a session with none.
	Unit     func(ctx context.Context, session string) (u unit.Unit, ok bool, err error)
	Proposer AgentProposer
}

var (
	// ErrNoAgentSession refuses a call that names no session.
	ErrNoAgentSession = errors.New("the call names no agent session")
	// ErrNoDraftUnit refuses a proposal that names no unit from a session with no turn.
	ErrNoDraftUnit = errors.New("unit: required, this session has no turn whose unit the draft would post on")
	errNoRev       = fmt.Errorf("%w: rev: required, the rev you read, at least 1", outbox.ErrInvalid)
)

func (a *AgentDrafts) caller(ctx context.Context, session string) (outbox.Caller, error) {
	if session == "" {
		return outbox.Caller{}, ErrNoAgentSession
	}
	c := outbox.Caller{Session: session}
	u, ok, err := a.Unit(ctx, session)
	if err != nil {
		return outbox.Caller{}, err
	}
	if ok {
		c.Units = []unit.Unit{u}
	}
	return c, nil
}

// Propose proposes p for session and reads back the draft it made.
func (a *AgentDrafts) Propose(ctx context.Context, session string, p api.AgentDraftProposal) (api.AgentDraft, error) {
	c, err := a.caller(ctx, session)
	if err != nil {
		return api.AgentDraft{}, err
	}
	var u unit.Unit
	switch {
	case p.Unit != "":
		if u, err = parseDraftUnit(p.Unit); err != nil {
			return api.AgentDraft{}, err
		}
	case len(c.Units) > 0:
		u = c.Units[0]
	default:
		return api.AgentDraft{}, ErrNoDraftUnit
	}
	if a.Proposer == nil {
		return api.AgentDraft{}, errors.New("this daemon proposes no agent drafts")
	}
	id, err := a.Proposer(ctx, session, u, p)
	if err != nil {
		return api.AgentDraft{}, err
	}
	if !slices.Contains(c.Units, u) {
		c.Units = append(c.Units, u)
	}
	return a.read(ctx, c, id)
}

// Read is draft id as session reads it.
func (a *AgentDrafts) Read(ctx context.Context, session string, id int64) (api.AgentDraft, error) {
	c, err := a.caller(ctx, session)
	if err != nil {
		return api.AgentDraft{}, err
	}
	return a.read(ctx, c, draft.ID(id))
}

// Update revises draft u.ID at u.Rev for session.
func (a *AgentDrafts) Update(ctx context.Context, session string, u api.AgentDraftUpdate) (api.AgentDraft, error) {
	c, err := a.caller(ctx, session)
	if err != nil {
		return api.AgentDraft{}, err
	}
	if u.Rev < 1 {
		return api.AgentDraft{}, errNoRev
	}
	am := outbox.Amendment{Text: u.Text, Basis: u.Basis, Rev: u.Rev, By: draft.Actor{Kind: draft.ActorSession, Session: c.Session}}
	if len(u.Payload) > 0 {
		payload := u.Payload
		am.Payload = &payload
	}
	if _, err := a.Outbox.AgentAmend(ctx, c, draft.ID(u.ID), am); err != nil {
		return api.AgentDraft{}, err
	}
	return a.read(ctx, c, draft.ID(u.ID))
}

// Withdraw withdraws draft w.ID at w.Rev for session; no reason is draft.WithdrawSuperseded.
func (a *AgentDrafts) Withdraw(ctx context.Context, session string, w api.AgentDraftWithdrawal) (api.AgentDraft, error) {
	c, err := a.caller(ctx, session)
	if err != nil {
		return api.AgentDraft{}, err
	}
	if w.Rev < 1 {
		return api.AgentDraft{}, errNoRev
	}
	reason := w.Reason
	if reason == "" {
		reason = draft.WithdrawSuperseded
	}
	if _, err := a.Outbox.AgentWithdraw(ctx, c, draft.ID(w.ID), w.Rev, reason); err != nil {
		return api.AgentDraft{}, err
	}
	return a.read(ctx, c, draft.ID(w.ID))
}

func (a *AgentDrafts) read(ctx context.Context, c outbox.Caller, id draft.ID) (api.AgentDraft, error) {
	d, revs, err := a.Outbox.AgentRead(ctx, c, id)
	if err != nil {
		return api.AgentDraft{}, err
	}
	return agentDraftOf(d, revs), nil
}

func agentDraftOf(d draft.Draft, revs []draft.Revision) api.AgentDraft {
	out := api.AgentDraft{ID: int64(d.ID), Rev: d.Rev, Unit: draftUnitArg(d.Unit), Kind: string(d.Kind), Delivery: string(d.Delivery),
		Target: d.Target, Text: d.Text, Payload: d.Payload, Basis: d.Basis, Hold: d.Hold, State: string(d.State), Error: d.Error,
		Reason: d.Reason, Permalink: d.Evidence.Permalink}
	for _, r := range revs {
		out.Revisions = append(out.Revisions, api.AgentDraftRevision{Rev: r.Rev, Text: r.Text, Payload: r.Payload, Basis: r.Basis,
			By: string(r.By.Kind), Session: r.By.Session, At: r.At})
	}
	return out
}

func draftUnitArg(u unit.Unit) string { return string(u.Provider) + "/" + u.Key }

func parseDraftUnit(raw string) (unit.Unit, error) {
	p, key, ok := strings.Cut(raw, "/")
	if !ok || !unit.ValidProvider(p) || key == "" {
		return unit.Unit{}, badRequest{"unit: " + raw + " is not <provider>/<key>"}
	}
	return unit.Unit{Provider: unit.ProviderID(p), Key: key}, nil
}

// handleAgentDrafts serves api.AgentDrafts for the session of the request's header.
func (s *Server) handleAgentDrafts(w http.ResponseWriter, r *http.Request) {
	session, ok := s.caller(w, r, true)
	if !ok {
		return
	}
	a := s.deps.AgentDrafts
	if a == nil {
		writeError(w, http.StatusServiceUnavailable, "the daemon keeps no drafts")
		return
	}
	ctx := r.Context()
	var got api.AgentDraft
	var err error
	switch r.Method {
	case http.MethodGet:
		id, perr := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if perr != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "id: required, a draft's id")
			return
		}
		got, err = a.Read(ctx, session, id)
	case http.MethodPost:
		var p api.AgentDraftProposal
		if !decodeAgentStrict(w, r, &p) {
			return
		}
		got, err = a.Propose(context.WithoutCancel(ctx), session, p)
	case http.MethodPatch:
		var u api.AgentDraftUpdate
		if !decodeAgentStrict(w, r, &u) || !draftIDGiven(w, u.ID, u.Rev) {
			return
		}
		got, err = a.Update(context.WithoutCancel(ctx), session, u)
	case http.MethodDelete:
		var wd api.AgentDraftWithdrawal
		if !decodeAgentStrict(w, r, &wd) || !draftIDGiven(w, wd.ID, wd.Rev) {
			return
		}
		got, err = a.Withdraw(context.WithoutCancel(ctx), session, wd)
	}
	if err != nil {
		writeAgentDraftError(w, err)
		return
	}
	writeJSON(w, got)
}

func draftIDGiven(w http.ResponseWriter, id, rev int64) bool {
	switch {
	case id <= 0:
		writeError(w, http.StatusBadRequest, "id: required, a draft's id")
		return false
	case rev <= 0:
		writeError(w, http.StatusBadRequest, "rev: required, the rev you read")
		return false
	}
	return true
}

// decodeAgentStrict decodes one JSON object into v and refuses a field v does not have, so a body that
// names a session is refused rather than ignored.
func decodeAgentStrict(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, agentBodyMax))
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err = dec.Decode(v); err == nil && dec.More() {
			err = errors.New("one JSON object per request")
		}
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return false
	}
	return true
}

func writeAgentDraftError(w http.ResponseWriter, err error) {
	var bad badRequest
	switch {
	case errors.Is(err, outbox.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, outbox.ErrOutOfScope):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, outbox.ErrRevConflict), errors.Is(err, outbox.ErrStale), errors.Is(err, outbox.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, outbox.ErrInvalid), errors.Is(err, ErrNoDraftUnit), errors.Is(err, ErrNoAgentSession), errors.As(err, &bad):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, fmt.Sprint(err))
	}
}
