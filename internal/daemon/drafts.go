package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/change"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/duration"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
)

const (
	draftsBodyMax = 64 << 10
	// A window draft whose deadline passed but that its gate refused is tried again after draftDueRetry, so the
	// sender never spins on it; with nothing due, the sender still wakes each draftWatchdog.
	draftDueRetry = 30 * time.Second
	draftWatchdog = 5 * time.Minute
	// draftWhereTimeout bounds one destination lookup and draftWhereBudget a page's; a lookup that failed or ran
	// out of time is not tried again for draftWhereRetry, so a held Slack never slows each refresh.
	draftWhereTimeout = 3 * time.Second
	draftWhereBudget  = 5 * time.Second
	draftWhereRetry   = time.Minute
)

var errOutboxUnavailable = unavailable{"the Outbox is not served: the daemon runs without a store"}

// mayHaveBeenPosted starts a publisher's error when the post may have reached its provider (#publisher).
const mayHaveBeenPosted = "may have been posted"

// draftVerbs are the entry verbs a draft takes, by the draft action each runs.
var draftVerbs = map[gtd.Verb]string{
	gtd.VerbApprove: "approve",
	gtd.VerbEdit:    "edit",
	gtd.VerbDismiss: "discard",
	gtd.VerbRetry:   "retry",
}

// registerOutbox serves the Outbox view from the drafts and makes a draft entry's verbs act on its draft.
func (s *Server) registerOutbox() {
	if s.deps.GTD == nil || s.deps.Outbox == nil {
		return
	}
	for verb := range draftVerbs {
		s.deps.GTD.Register(gtd.KindDraft, verb, s.draftVerb(verb))
	}
	s.deps.GTD.RegisterView(gtd.ViewOutbox, draftPrefix, outboxView{s})
}

func (s *Server) handleDrafts(w http.ResponseWriter, r *http.Request) {
	ctx := whereBy(r.Context(), time.Now().Add(s.whereBudget()))
	if s.deps.Outbox == nil {
		writeActionError(w, errOutboxUnavailable)
		return
	}
	q := r.URL.Query()
	f := outbox.Filter{States: []draft.State{draft.Proposed, draft.Failed}, Entry: q.Get("entry")}
	if raw := q.Get("state"); raw != "" {
		f.States = nil
		for _, st := range strings.Split(raw, ",") {
			st = strings.TrimSpace(st)
			if !slices.Contains(draft.States, draft.State(st)) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown draft state %q", st))
				return
			}
			f.States = append(f.States, draft.State(st))
		}
	}
	ds, err := s.deps.Outbox.List(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, api.DraftsResponse{Drafts: s.draftDetails(ctx, ds)})
}

func (s *Server) handleDraft(w http.ResponseWriter, r *http.Request) {
	if s.deps.Outbox == nil {
		writeActionError(w, errOutboxUnavailable)
		return
	}
	id, ok := parseDraftID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no draft "+strconv.Quote(r.PathValue("id")))
		return
	}
	d, err := s.deps.Outbox.Store.Get(r.Context(), id)
	if err != nil {
		writeDraftError(w, err)
		return
	}
	writeJSON(w, s.draftDetail(r.Context(), d, nil))
}

func (s *Server) handleDraftAction(w http.ResponseWriter, r *http.Request) {
	if s.deps.Outbox == nil {
		writeActionError(w, errOutboxUnavailable)
		return
	}
	action := r.PathValue("action")
	if !slices.Contains(api.DraftActions, action) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown draft action %q; one of %s", action, strings.Join(api.DraftActions, ", ")))
		return
	}
	id, ok := parseDraftID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no draft "+strconv.Quote(r.PathValue("id")))
		return
	}
	var body api.DraftAction
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, draftsBodyMax)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	d, err := s.draftDo(context.WithoutCancel(r.Context()), id, action, body)
	if err != nil {
		writeDraftError(w, err)
		return
	}
	writeJSON(w, s.draftDetail(r.Context(), d, nil))
}

func parseDraftID(raw string) (draft.ID, bool) {
	n, err := strconv.ParseInt(raw, 10, 64)
	return draft.ID(n), err == nil && n > 0
}

// noPublisher refuses a draft whose provider's plugin is enabled but publishes nothing.
type noPublisher struct {
	provider unit.ProviderID
	err      error
}

func (n noPublisher) Error() string { return n.err.Error() }
func (n noPublisher) Unwrap() error { return n.err }

// draftDo runs action on draft id: refused with pluginOff when the draft's plugin is disabled, but for discard.
func (s *Server) draftDo(ctx context.Context, id draft.ID, action string, a api.DraftAction) (draft.Draft, error) {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	o := s.deps.Outbox
	d, err := o.Store.Get(ctx, id)
	if err != nil {
		return draft.Draft{}, err
	}
	if action != "discard" && !s.pluginEnabled(d.Unit.Provider) {
		return draft.Draft{}, pluginOff{d.Unit.Provider}
	}
	if a.Rev != nil && *a.Rev != d.Rev {
		return draft.Draft{}, fmt.Errorf("%w: draft %d changed since you saw it (rev %d, now %d)", outbox.ErrConflict, id, *a.Rev, d.Rev)
	}
	var got draft.Draft
	switch action {
	case "approve":
		if a.Rev == nil {
			return draft.Draft{}, badRequest{"approve takes the rev you saw"}
		}
		if a.Text == nil {
			a.Text = &d.Text
		}
		got, err = o.Approve(ctx, id, a.Text)
	case "edit":
		if a.Text == nil {
			return draft.Draft{}, badRequest{"edit takes the draft's new text"}
		}
		got, err = o.Edit(ctx, id, *a.Text)
	case "discard":
		got, err = o.Discard(ctx, id, a.Reason)
	case "retry":
		if a.Rev == nil {
			return draft.Draft{}, badRequest{"retry takes the rev you saw"}
		}
		got, err = o.Retry(ctx, id, a.Confirm)
	default:
		return draft.Draft{}, badRequest{fmt.Sprintf("unknown draft action %q", action)}
	}
	if errors.Is(err, outbox.ErrDisabled) {
		err = noPublisher{d.Unit.Provider, err}
	}
	return got, err
}

func writeDraftError(w http.ResponseWriter, err error) {
	var off pluginOff
	var none noPublisher
	switch {
	case errors.As(err, &off):
		writePluginDisabled(w, off.provider)
	case errors.As(err, &none):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": none.Error(), "plugin": string(none.provider)})
	case errors.Is(err, outbox.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, outbox.ErrNeedsConfirm):
		writeError(w, http.StatusConflict, err.Error()+": pass confirm once you checked it was not posted")
	case errors.Is(err, outbox.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, outbox.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeActionError(w, err)
	}
}

// draftVerb runs verb on a draft entry's draft, with the statuses the draft routes answer.
func (s *Server) draftVerb(verb gtd.Verb) coregtd.VerbHandler {
	return func(ctx context.Context, e gtd.Entry, req coregtd.Request) (coregtd.Result, error) {
		id, ok := draftIDOf(e.ID)
		if !ok {
			return coregtd.Result{}, fmt.Errorf("%w: %s", coregtd.ErrNoEntry, e.ID)
		}
		var a api.DraftAction
		if text, ok := req.Args["text"]; ok {
			a.Text = &text
		}
		a.Reason = req.Args["reason"]
		if raw, ok := req.Args["confirm"]; ok {
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return coregtd.Result{}, fmt.Errorf("%w: confirm is true or false, not %q", coregtd.ErrArgs, raw)
			}
			a.Confirm = b
		}
		if raw, ok := req.Args["rev"]; ok {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < 0 {
				return coregtd.Result{}, fmt.Errorf("%w: rev is the draft's revision, not %q", coregtd.ErrArgs, raw)
			}
			a.Rev = &n
		}
		d, err := s.draftDo(ctx, id, draftVerbs[verb], a)
		if err != nil {
			return coregtd.Result{}, entryDraftError(err)
		}
		got, err := s.deps.GTD.Entry(ctx, e.ID)
		if err != nil {
			return coregtd.Result{}, err
		}
		return coregtd.Result{Entry: got, Lines: []string{fmt.Sprintf("draft %d is %s", d.ID, d.State)}}, nil
	}
}

// entryDraftError is a draft action's error as the entries' routes answer it.
func entryDraftError(err error) error {
	var off pluginOff
	var bad badRequest
	switch {
	case errors.As(err, &off):
		return fmt.Errorf("%w: %s", coregtd.ErrPluginDisabled, off)
	case errors.Is(err, outbox.ErrNotFound):
		return fmt.Errorf("%w: %w", coregtd.ErrNoEntry, err)
	case errors.Is(err, outbox.ErrNeedsConfirm):
		return fmt.Errorf("%w: %w: pass the confirm argument once you checked it was not posted", coregtd.ErrVerb, err)
	case errors.Is(err, outbox.ErrConflict), errors.Is(err, outbox.ErrDisabled):
		return fmt.Errorf("%w: %w", coregtd.ErrVerb, err)
	case errors.Is(err, outbox.ErrInvalid), errors.As(err, &bad):
		return fmt.Errorf("%w: %w", coregtd.ErrArgs, err)
	}
	return err
}

// draftView is d on the wire, with its actions as they stand.
func (s *Server) draftView(ctx context.Context, d draft.Draft) api.Draft {
	v := api.Draft{ID: int64(d.ID), Unit: string(d.Unit.Provider) + "/" + d.Unit.Key, Items: slices.Clone(d.Items), Entry: d.Entry, Kind: string(d.Kind),
		Target: d.Target, Text: d.Text, Reaction: d.Reaction, Original: d.Original, Replaced: d.Replaced, Edited: d.Edited,
		Origin: api.DraftOrigin{Kind: string(d.Origin.Kind), TurnSeq: d.Origin.TurnSeq, Intent: d.Origin.Intent,
			Workflow: d.Origin.Workflow, Rule: d.Origin.Rule, Unit: originUnit(d.Origin)},
		Approval: api.DraftApproval{Mode: string(d.Approval.Mode)},
		State:    string(d.State), Error: d.Error, Reason: d.Reason, Permalink: d.Evidence.Permalink,
		Would: api.DraftWould{Policy: string(d.Would.Policy), Reason: d.Would.Reason, Outcome: string(d.Would.Outcome)},
		Rev:   d.Rev, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
	v.Purpose, v.Delivery, v.Effects = d.Purpose, string(d.Delivery), slices.Clone(d.Effects)
	if v.Items == nil {
		v.Items = []string{}
	}
	if v.Effects == nil {
		v.Effects = []string{}
	}
	if d.Approval.Window > 0 {
		v.Approval.Window = duration.Format(d.Approval.Window)
	}
	if !d.Deadline.IsZero() {
		dl := d.Deadline
		v.Deadline = &dl
	}
	f := catalog.DraftFacts{State: v.State, Provider: d.Unit.Provider, Disabled: !s.pluginEnabled(d.Unit.Provider),
		MayHaveBeenPosted: d.Error == outbox.NotConfirmed || strings.HasPrefix(d.Error, mayHaveBeenPosted)}
	if o := s.deps.Outbox; o != nil && o.Effects != nil {
		_, err := o.Effects(d.Unit.Provider)
		f.NoPublisher = err != nil
	}
	v.Actions = catalog.DraftStates(s.loaded(), f)
	v.Where = s.draftWhere(ctx, d)
	p := s.presentations([]unit.Unit{d.Unit})[d.Unit]
	v.Label, v.Title, v.Link = p.Label, p.Title, p.Link
	return v
}

var mentionKey = regexp.MustCompile(`@\{([^{}\s]+)\}`)

// draftDetail is draftView with the names of the people the text mentions and the items it is about, read
// from the unit's items; its is those items, nil to read them.
func (s *Server) draftDetail(ctx context.Context, d draft.Draft, its []items.Item) api.Draft {
	v := s.draftView(ctx, d)
	if s.deps.Items == nil || (len(d.Items) == 0 && !mentionKey.MatchString(d.Text)) {
		return v
	}
	if its == nil {
		var err error
		if its, err = s.deps.Items.Items(ctx, d.Unit); err != nil {
			s.logf("drafts: the items of %s: %v", d.Unit.Label(), err)
			return v
		}
	}
	names := map[string]string{}
	for _, it := range its {
		for _, p := range append(append([]items.Person{it.Requester}, it.Assignees...), it.WaitingOn...) {
			for _, k := range append([]string{p.Key}, p.Aliases...) {
				if _, ok := names[k]; !ok && p.Name != "" && k != "" {
					names[k] = p.Name
				}
			}
		}
	}
	for _, m := range mentionKey.FindAllStringSubmatch(d.Text, -1) {
		if n, ok := names[m[1]]; ok {
			if v.Names == nil {
				v.Names = map[string]string{}
			}
			v.Names[m[1]] = n
		}
	}
	for _, id := range d.Items {
		iv := api.ItemView{Unit: d.Unit, ID: id}
		if i := slices.IndexFunc(its, func(it items.Item) bool { return it.ID == id }); i >= 0 {
			iv.Summary, iv.State, iv.Kind = its[i].Summary, string(its[i].State), string(its[i].Kind)
		}
		v.ItemDetails = append(v.ItemDetails, iv)
	}
	return v
}

// draftDetails are the details of ds, each unit's items read once.
func (s *Server) draftDetails(ctx context.Context, ds []draft.Draft) []api.Draft {
	byUnit := map[unit.Unit][]items.Item{}
	out := make([]api.Draft, 0, len(ds))
	for _, d := range ds {
		its, ok := byUnit[d.Unit]
		if !ok && s.deps.Items != nil {
			var err error
			if its, err = s.deps.Items.Items(ctx, d.Unit); err != nil {
				s.logf("drafts: the items of %s: %v", d.Unit.Label(), err)
			}
			if its == nil {
				its = []items.Item{}
			}
			byUnit[d.Unit] = its
		}
		out = append(out, s.draftDetail(ctx, d, its))
	}
	return out
}

// draftWhere is where a draft still to be posted goes, best effort: "" when its publisher cannot tell.
// A unit and target never change, so a resolved destination is kept for the daemon's life; a failed lookup, or one
// the page had no time left for, is kept for draftWhereRetry.
func (s *Server) draftWhere(ctx context.Context, d draft.Draft) string {
	if !slices.Contains(draft.Open, d.State) && d.State != draft.Failed {
		return ""
	}
	now := time.Now()
	if c, ok := s.draftWheres.Load(d.ID); ok {
		w := c.(whereSeen)
		if w.where != "" || now.Sub(w.failedAt) < draftWhereRetry {
			return w.where
		}
	}
	loc := s.locator(d.Unit.Provider)
	if loc == nil {
		return ""
	}
	by := now.Add(s.whereTimeout())
	if page, ok := ctx.Value(whereByKey{}).(time.Time); ok && page.Before(by) {
		by = page
	}
	if !now.Before(by) {
		s.draftWheres.Store(d.ID, whereSeen{failedAt: now})
		return ""
	}
	ctx, cancel := context.WithDeadline(ctx, by)
	defer cancel()
	w, err := loc.Where(ctx, d)
	if err != nil || w == "" {
		s.draftWheres.Store(d.ID, whereSeen{failedAt: time.Now()})
		return ""
	}
	s.draftWheres.Store(d.ID, whereSeen{where: w})
	return w
}

type whereSeen struct {
	where    string
	failedAt time.Time
}

type whereByKey struct{}

// whereBy makes by the deadline of every draft destination lookup under ctx.
func whereBy(ctx context.Context, by time.Time) context.Context {
	return context.WithValue(ctx, whereByKey{}, by)
}

func (s *Server) locator(p unit.ProviderID) port.Locator {
	o := s.deps.Outbox
	if o == nil || o.Effects == nil {
		return nil
	}
	fx, err := o.Effects(p)
	if err != nil {
		return nil
	}
	loc, _ := fx.(port.Locator)
	return loc
}

func (s *Server) whereTimeout() time.Duration { return cmp.Or(s.whereLookup, draftWhereTimeout) }

func (s *Server) whereBudget() time.Duration { return cmp.Or(s.wherePage, draftWhereBudget) }

// sendDrafts settles the drafts a crash left sending, then publishes the approved drafts, and approves the window
// drafts that came due, after each draft note and at the next deadline, until ctx ends.
func (s *Server) sendDrafts(ctx context.Context) {
	o := s.deps.Outbox
	var poke <-chan struct{}
	if s.deps.Changes != nil {
		p, cancel := change.Poke(s.deps.Changes, port.TopicDrafts)
		defer cancel()
		poke = p
	}
	rctx, scope := meter.Start(ctx, meter.Loop("drafts"))
	if err := o.Recover(rctx); err != nil && ctx.Err() == nil {
		s.logf("drafts: recover: %v", err)
	}
	scope.End()
	for {
		pctx, scope := meter.Start(ctx, meter.Loop("drafts"))
		if _, err := o.Tick(pctx); err != nil && ctx.Err() == nil {
			s.logf("drafts: tick: %v", err)
		}
		sent, failed, err := o.Send(pctx)
		if err != nil && ctx.Err() == nil {
			s.logf("drafts: send: %v", err)
		}
		if sent+failed > 0 {
			s.logf("drafts: %d sent, %d failed", sent, failed)
		}
		wait := s.nextDraftWake(pctx)
		scope.End()
		select {
		case <-ctx.Done():
			return
		case <-poke:
		case <-s.draftClock.After(wait):
		}
	}
}

// nextDraftWake is how long the sender may sleep: until the next window deadline, draftDueRetry when one is
// already past, and no longer than draftWatchdog.
func (s *Server) nextDraftWake(ctx context.Context) time.Duration {
	next, ok, err := s.deps.Outbox.NextDeadline(ctx)
	if err != nil {
		s.logf("drafts: next deadline: %v", err)
	}
	if !ok {
		return draftWatchdog
	}
	wait := next.Sub(s.deps.Outbox.Clock.Now())
	if wait <= 0 {
		return draftDueRetry
	}
	return min(wait, draftWatchdog)
}

func originUnit(o draft.Origin) string {
	if o.Unit.IsZero() {
		return ""
	}
	return o.Unit.Label()
}
