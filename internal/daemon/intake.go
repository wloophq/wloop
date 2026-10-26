package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

const (
	sourceCLI     = "cli"
	sourceCockpit = "cockpit"
	sourceMenubar = "menubar"
	sourceTUI     = "tui"
	sourceSession = prrow.SourceSession
)

const (
	noteSessionStopped  = "the session stopped asking"
	noteDaemonRestarted = "the daemon restarted while the session waited; ask again from the session"
)

var intakeSources = []string{sourceCLI, sourceCockpit, sourceMenubar, sourceTUI}

const startHeldRound = "starting the held round"

var errIntakesUnavailable = errors.New("the daemon keeps no intakes")

// AfterTurn in a payload is the newest turn of the pull request when the
// request was accepted, 0 for none: a turn the sweep sent past it answered
// the request.
type actionPayload struct {
	Action    string `json:"action"`
	AfterTurn int64  `json:"after_turn"`
	api.ActionRequest
}

type byHandPayload struct {
	Handle    string `json:"handle,omitempty"`
	AfterTurn int64  `json:"after_turn"`
	api.ByHandRequest
}

// decided is what the race with the sweep reads of any payload.
type decided struct {
	AfterTurn int64          `json:"after_turn"`
	Guidance  string         `json:"guidance"`
	Inputs    map[string]any `json:"inputs"`
}

type byHandOutcome struct {
	res api.ByHandResult
	err error
}

type loopHeldKey struct{}

type briefMissingKey struct{}

// BriefMissing is why the turn an intake starts under ctx goes without a
// brief; "" when it has one or ctx is no intake's.
func BriefMissing(ctx context.Context) string {
	why, _ := ctx.Value(briefMissingKey{}).(string)
	return why
}

// withLoopHeld marks ctx as run by a caller that holds the loop mutex.
func withLoopHeld(ctx context.Context) context.Context {
	return context.WithValue(ctx, loopHeldKey{}, true)
}

// LoopHeld tells a context whose caller holds the daemon's loop mutex, as
// an intake's start does: what runs under it must not take the mutex again.
func LoopHeld(ctx context.Context) bool {
	held, _ := ctx.Value(loopHeldKey{}).(bool)
	return held
}

func (s *Server) newIntakes(d Deps) *intake.Service {
	if d.Intakes == nil {
		return nil
	}
	return &intake.Service{Store: d.Intakes, Briefs: intakeBriefs(d), Firsts: intakeFirsts{s}, Starter: intakeStarter{s}, Payloads: guidancePayloads{},
		Clock: clockFunc(d.Now), Pub: intakePub{s}, Lock: d.LoopMu, Log: s.logf, Needs: briefNeeds(d)}
}

// briefNeeds are the brief fields an intent's manifest needs.
func briefNeeds(d Deps) func(string) []string {
	if d.Intents == nil {
		return nil
	}
	return func(name string) []string {
		def, _ := d.Intents().Registry.Get(dintent.Name(name))
		return def.Brief.Need
	}
}

type clockFunc func() time.Time

func (c clockFunc) Now() time.Time { return c() }

func intakeBriefs(d Deps) intake.Briefs {
	switch {
	case d.IntakeBriefs != nil:
		return d.IntakeBriefs
	case d.BriefsUnavailable != "":
		return unavailableBriefs{why: d.BriefsUnavailable}
	}
	return offBriefs{}
}

// unavailableBriefs starts every intake at once without a brief the
// daemon cannot make, saying why.
type unavailableBriefs struct {
	offBriefs
	why string
}

func (u unavailableBriefs) Ready(context.Context, unit.Unit, bool, []string) (intake.Readiness, error) {
	return intake.Readiness{Go: true, Without: true, Note: "briefs are unavailable: " + u.why}, nil
}

// offBriefs starts every intake at once, as briefs that are off.
type offBriefs struct{}

func (offBriefs) Ready(context.Context, unit.Unit, bool, []string) (intake.Readiness, error) {
	return intake.Readiness{Go: true, Without: true, Cause: intake.CauseOff}, nil
}

func (offBriefs) Changed(unit.Unit) (<-chan struct{}, func()) { return nil, func() {} }

func (offBriefs) Unwant(context.Context, unit.Unit) error { return nil }

// intakeFirsts tells a first turn: its book holds no turn but withdrawn
// ones.
type intakeFirsts struct{ s *Server }

func (f intakeFirsts) First(ctx context.Context, u unit.Unit) (bool, error) {
	if f.s.deps.Turns == nil {
		return false, nil
	}
	books, err := f.s.deps.Turns.Store.Books(ctx, u)
	if err != nil {
		return false, err
	}
	return !slices.ContainsFunc(books[u].Turns, func(t turn.Turn) bool { return t.State != turn.Withdrawn }), nil
}

type guidancePayloads struct{}

func (guidancePayloads) WithGuidance(payload []byte, guidance string) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil, err
	}
	g, err := json.Marshal(guidance)
	if err != nil {
		return nil, err
	}
	m["guidance"] = g
	return json.Marshal(m)
}

// intakeWaiters wakes the long polls of an intake on each of its events.
type intakeWaiters struct {
	mu   sync.Mutex
	subs map[int64][]chan struct{}
}

func (w *intakeWaiters) watch(seq int64) (<-chan struct{}, func()) {
	ch := make(chan struct{})
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.subs == nil {
		w.subs = map[int64][]chan struct{}{}
	}
	w.subs[seq] = append(w.subs[seq], ch)
	return ch, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		subs := w.subs[seq]
		if i := slices.Index(subs, ch); i >= 0 {
			w.subs[seq] = slices.Delete(subs, i, i+1)
		}
		if len(w.subs[seq]) == 0 {
			delete(w.subs, seq)
		}
	}
}

func (w *intakeWaiters) wake(seq int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, ch := range w.subs[seq] {
		close(ch)
	}
	delete(w.subs, seq)
}

// sessionAsks keeps when each session intake was last asked for by its
// session, and which one a session waits on now. gate serializes the
// expiry with a session's accept, so a join is never expired under it.
type sessionAsks struct {
	gate     sync.Mutex
	mu       sync.Mutex
	last     map[int64]time.Time
	asking   map[int64]bool
	starting map[int64]func()
}

// begin marks seq asked for now; onStarting runs once it starts.
func (a *sessionAsks) begin(seq int64, onStarting func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.asking == nil {
		a.asking, a.last, a.starting = map[int64]bool{}, map[int64]time.Time{}, map[int64]func(){}
	}
	a.asking[seq], a.starting[seq] = true, onStarting
}

func (a *sessionAsks) started(seq int64) {
	a.mu.Lock()
	f := a.starting[seq]
	delete(a.starting, seq)
	a.mu.Unlock()
	if f != nil {
		f()
	}
}

func (a *sessionAsks) end(seq int64, at time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.asking[seq] {
		return
	}
	delete(a.asking, seq)
	delete(a.starting, seq)
	a.last[seq] = at
}

// stale tells a session intake nobody asked for since before cutoff.
func (a *sessionAsks) stale(i intake.Intake, cutoff time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.asking[i.Seq] {
		return false
	}
	last, ok := a.last[i.Seq]
	if !ok {
		last = i.AcceptedAt
	}
	return last.Before(cutoff)
}

func (a *sessionAsks) forget(seq int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.asking, seq)
	delete(a.last, seq)
	delete(a.starting, seq)
}

type intakePub struct{ s *Server }

func (p intakePub) Publish(evs ...port.Event) {
	for _, ev := range evs {
		key, _, _ := strings.Cut(ev.Key, ":")
		if seq, err := strconv.ParseInt(key, 10, 64); err == nil {
			p.s.intakeWaiters.wake(seq)
			if ev.Kind == intake.KindStarting {
				p.s.sessionAsks.started(seq)
			}
		}
	}
}

// intakeStarter runs the action of an intake under the loop mutex the
// service holds. Its context outlives the caller's: an action that began
// runs to its end.
type intakeStarter struct{ s *Server }

// ReadAhead reads the intake's pull request before the service takes the
// loop mutex for its start.
func (st intakeStarter) ReadAhead(ctx context.Context, i intake.Intake) context.Context {
	return st.s.readAhead(ctx, i.Unit, false)
}

func (st intakeStarter) Start(ctx context.Context, i intake.Intake, out io.Writer) (intake.StartedTurn, error) {
	s := st.s
	ctx = withLoopHeld(context.WithoutCancel(ctx))
	if i.Brief != intake.BriefReady && i.Note != "" {
		ctx = context.WithValue(ctx, briefMissingKey{}, i.Note)
	}
	if t, ok, err := s.sentWhileWaiting(ctx, i); err != nil || ok {
		if ok {
			err = s.startedBySweep(i, t, out)
		}
		return intake.StartedTurn{TurnSeq: t.Seq, TurnID: t.ID}, err
	}
	before, err := s.newestTurn(ctx, i.Unit)
	if err != nil {
		return intake.StartedTurn{}, err
	}
	s.briefLine(ctx, i, out)
	if i.Source == sourceSession {
		err = s.startByHand(ctx, i, out)
	} else {
		err = s.startAction(ctx, i, out)
	}
	if err != nil {
		return intake.StartedTurn{}, err
	}
	return s.turnAfter(ctx, i.Unit, before)
}

// sentWhileWaiting is the automated turn of the intake's intent the sweep
// sent past the newest turn at accept: the round the intake asks for went
// out without it.
func (s *Server) sentWhileWaiting(ctx context.Context, i intake.Intake) (turn.Turn, bool, error) {
	if s.deps.Turns == nil {
		return turn.Turn{}, false, nil
	}
	var d decided
	if err := json.Unmarshal(i.Request, &d); err != nil {
		return turn.Turn{}, false, fmt.Errorf("the request of intake %d: %w", i.Seq, err)
	}
	books, err := s.deps.Turns.Store.Books(ctx, i.Unit)
	if err != nil {
		return turn.Turn{}, false, err
	}
	b := books[i.Unit]
	t, ok := b.Newest(i.Intent)
	if !ok || t.Seq <= d.AfterTurn || t.Trigger != turn.Automated {
		return turn.Turn{}, false, nil
	}
	return *t, true, nil
}

// startedBySweep ends the intake on the sweep's turn, unless the request
// carried guidance that turn did not, which it fails so the user asks again.
func (s *Server) startedBySweep(i intake.Intake, t turn.Turn, out io.Writer) error {
	var d decided
	_ = json.Unmarshal(i.Request, &d)
	sent := fmt.Sprintf("the sweep sent the %s of %s (round %s) while this request waited", i.Intent, i.Unit.Label(), t.ID)
	def, _ := s.loaded().Registry.Get(dintent.Name(i.Intent))
	var err error
	switch how := sweepLacks(def, d.Inputs, t.Inputs); {
	case d.Guidance != "" && d.Guidance != t.Guidance:
		err = conflict{sent + "; your guidance was not sent, ask again once that turn ends"}
	case how != "":
		err = conflict{sent + " " + how + "; ask again once that turn ends"}
	default:
		fmt.Fprintln(out, sent+"; nothing more was sent")
	}
	if i.Source == sourceSession {
		outcome := err
		if outcome == nil {
			outcome = conflict{sent + "; run this command again to follow that turn"}
		}
		s.byHand.Store(i.Seq, byHandOutcome{err: outcome})
	}
	return err
}

// sweepLacks says which input of the request's join.differs_on the
// sweep's turn went without, "" when it carries them all.
func sweepLacks(d dintent.Def, asked, sent map[string]any) string {
	asked, _ = d.NormalizeAll(asked)
	sent, _ = d.NormalizeAll(sent)
	for _, name := range d.Join.DiffersOn {
		if dintent.Zero(asked[name]) || reflect.DeepEqual(asked[name], sent[name]) {
			continue
		}
		if i := slices.IndexFunc(d.Inputs, func(x dintent.Input) bool { return x.Name == name }); i >= 0 {
			return "without " + d.Inputs[i].Label()
		}
	}
	return ""
}

func (s *Server) startAction(ctx context.Context, i intake.Intake, out io.Writer) error {
	var p actionPayload
	if err := json.Unmarshal(i.Request, &p); err != nil {
		return fmt.Errorf("the request of intake %d: %w", i.Seq, err)
	}
	line := startHeldRound
	if d, ok := s.loaded().Registry.Get(dintent.Name(p.Action)); ok {
		line = d.StartLine
	}
	fmt.Fprintln(out, line)
	err := s.act(ctx, p.Action, i.Unit, p.ActionRequest, out)
	var refused Refusal
	if errors.As(err, &refused) {
		s.logDetail(p.Action, i.Unit, refused)
	}
	return err
}

func (s *Server) startByHand(ctx context.Context, i intake.Intake, out io.Writer) error {
	var p byHandPayload
	if err := json.Unmarshal(i.Request, &p); err != nil {
		return fmt.Errorf("the request of intake %d: %w", i.Seq, err)
	}
	res, err := s.deps.TurnByHand(ctx, p.ByHandRequest, p.Handle)
	for _, n := range res.Notes {
		fmt.Fprintln(out, n)
	}
	s.byHand.Store(i.Seq, byHandOutcome{res: res, err: err})
	return err
}

func (s *Server) newestTurn(ctx context.Context, u unit.Unit) (int64, error) {
	if s.deps.Turns == nil {
		return 0, nil
	}
	books, err := s.deps.Turns.Store.Books(ctx, u)
	if err != nil {
		return 0, err
	}
	var seq int64
	for _, t := range books[u].Turns {
		seq = max(seq, t.Seq)
	}
	return seq, nil
}

// turnAfter is the newest turn the action sent: one of a seq past before.
func (s *Server) turnAfter(ctx context.Context, u unit.Unit, before int64) (intake.StartedTurn, error) {
	if s.deps.Turns == nil {
		return intake.StartedTurn{}, nil
	}
	books, err := s.deps.Turns.Store.Books(ctx, u)
	if err != nil {
		return intake.StartedTurn{}, err
	}
	b := books[u]
	t, ok := b.Newest()
	if !ok || t.Seq <= before {
		return intake.StartedTurn{}, nil
	}
	return intake.StartedTurn{TurnSeq: t.Seq, TurnID: t.ID}, nil
}

// briefLine tells, for an intake that starts with its brief, how long the
// brief took and what it says.
func (s *Server) briefLine(ctx context.Context, i intake.Intake, out io.Writer) {
	if i.Brief != intake.BriefReady || s.deps.Records == nil {
		return
	}
	rec, ok, err := s.deps.Records.BriefRecord(ctx, i.Unit)
	var b brief.Brief
	if err != nil || !ok || json.Unmarshal([]byte(rec.Brief), &b) != nil {
		return
	}
	took := ""
	if run, ok, err := s.deps.Records.BriefRun(ctx, rec.Run); err == nil && ok && !run.EndedAt.IsZero() {
		took = fmt.Sprintf(" in %.1f s", run.EndedAt.Sub(run.StartedAt).Seconds())
	}
	line := fmt.Sprintf("brief ready%s: %s, size %s, risk %s", took, b.Kind, b.Size, b.Risk)
	if len(b.Tags) > 0 {
		line += "; " + strings.Join(b.Tags, ", ")
	}
	fmt.Fprintln(out, line)
}

// drive takes the intake to its end on the server's lifetime.
func (s *Server) drive(seq int64) {
	s.driving.Go(func() {
		var err error
		phase(s.life, meter.Loop("intake"), func(ctx context.Context) { _, err = s.intakes.Drive(ctx, seq) })
		if err != nil && !errors.Is(err, intake.ErrDriven) && s.life.Err() == nil {
			s.logf("intake %d: %v", seq, err)
		}
	})
}

// recoverIntakes fails the intakes a stop left starting, cancels the
// session ones, whose session stopped waiting with the daemon, and drives
// the others again.
func (s *Server) recoverIntakes(ctx context.Context) {
	seqs, err := s.intakes.Recover(ctx)
	if err != nil {
		s.logf("intakes: recovering: %v", err)
	}
	for _, seq := range seqs {
		i, ok, err := s.deps.Intakes.Intake(ctx, seq)
		switch {
		case err != nil || !ok:
		case i.Source == sourceSession:
			if err := s.intakes.CancelFor(ctx, seq, noteDaemonRestarted); err != nil {
				s.logf("intake %d: %v", seq, err)
			}
			s.sessionAsks.forget(seq)
		default:
			s.drive(seq)
		}
	}
}

// openIntakes lists the open intakes, the session ones no session asked
// for in twice the session wait left out; expireSessionIntakes cancels
// those.
func (s *Server) openIntakes(ctx context.Context) ([]intake.Intake, error) {
	return s.openIntakesAt(ctx, s.deps.Now())
}

// rowIntakes are openIntakes as of the minute's start, so a snapshot and a
// fresh derive of the same minute agree.
func (s *Server) rowIntakes(ctx context.Context) ([]intake.Intake, error) {
	return s.openIntakesAt(ctx, s.deps.Now().Truncate(time.Minute))
}

func (s *Server) openIntakesAt(ctx context.Context, now time.Time) ([]intake.Intake, error) {
	open, err := s.deps.Intakes.Open(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(open, s.staleSessionIntake(now)), nil
}

func (s *Server) staleSessionIntake(now time.Time) func(intake.Intake) bool {
	cutoff := now.Add(-2 * s.sessionWait)
	return func(i intake.Intake) bool {
		return i.Source == sourceSession && i.State.Open() && i.State != intake.Starting && s.sessionAsks.stale(i, cutoff)
	}
}

// expireSessionIntakes cancels the session intakes no session asked for in
// twice the session wait.
func (s *Server) expireSessionIntakes(ctx context.Context) error {
	s.sessionAsks.gate.Lock()
	defer s.sessionAsks.gate.Unlock()
	return s.expireSessionIntakesLocked(ctx)
}

func (s *Server) expireSessionIntakesLocked(ctx context.Context) error {
	open, err := s.deps.Intakes.Open(ctx)
	if err != nil {
		return err
	}
	stale := s.staleSessionIntake(s.deps.Now())
	for _, i := range open {
		if !stale(i) {
			continue
		}
		if err := s.intakes.CancelFor(ctx, i.Seq, noteSessionStopped); err != nil {
			s.logf("intake %d: %v", i.Seq, err)
			continue
		}
		s.sessionAsks.forget(i.Seq)
	}
	return nil
}

// intentOf is the intent an intake action asks for: run_now's is the one
// run.resolve picks for the row.
func (s *Server) intentOf(ctx context.Context, action string, row api.Row) (turn.Intent, error) {
	if action != "run_now" {
		return turn.Intent(action), nil
	}
	login, _, err := s.viewerInfo(ctx)
	if err != nil {
		return "", err
	}
	p, _ := prrow.PayloadOf(row)
	mine := login != "" && strings.EqualFold(p.Author, login)
	name, err := catalog.RunNowIntent(s.loaded().Registry, p.Workspace, p.Override, mine)
	if err != nil {
		return "", conflict{err.Error()}
	}
	return turn.Intent(name), nil
}

type conflict struct{ msg string }

func (e conflict) Error() string { return e.msg }

func joinRefused(open intake.Intake, how string) error {
	return conflict{catalog.JoinRefusalText(open.Seq, string(open.Intent), how)}
}

// joinsAction refuses a request from the command line, the cockpit, the
// menu bar or the TUI to join a turn by hand, an intake of another action,
// or one whose inputs differ on the intent's join.differs_on.
func joinsAction(action string, d dintent.Def, inputs map[string]any) func(open intake.Intake) error {
	return func(open intake.Intake) error {
		if open.Source == sourceSession {
			return joinRefused(open, "from an agent session")
		}
		var p actionPayload
		if err := json.Unmarshal(open.Request, &p); err != nil {
			return err
		}
		if p.Action != action {
			return joinRefused(open, "as a "+p.Action)
		}
		if how := catalog.JoinDiffers(d, p.Inputs, inputs); how != "" {
			return joinRefused(open, how)
		}
		return nil
	}
}

// joinsSession refuses a turn by hand to join an intake that is not the
// same session's, or one whose inputs differ on join.differs_on.
func joinsSession(handle string, d dintent.Def, inputs map[string]any) func(open intake.Intake) error {
	return func(open intake.Intake) error {
		if open.Source != sourceSession {
			return joinRefused(open, "from "+open.Source)
		}
		var p byHandPayload
		if err := json.Unmarshal(open.Request, &p); err != nil {
			return err
		}
		if p.Handle != handle {
			return joinRefused(open, "from another session")
		}
		if how := catalog.JoinDiffers(d, p.Inputs, inputs); how != "" {
			return joinRefused(open, how)
		}
		return nil
	}
}

// acceptAction answers a review, an address or a run with the intake that
// accepted it, which the server then drives.
func (s *Server) acceptAction(w http.ResponseWriter, r *http.Request, name string, row api.Row, req api.ActionRequest) {
	u := row.Unit
	if s.deps.Actions == nil {
		writeActionError(w, errActionsUnavailable)
		return
	}
	source := req.Source
	ctx := context.WithoutCancel(r.Context())
	if err := s.expireSessionIntakes(ctx); err != nil {
		writeActionError(w, err)
		return
	}
	intent, err := s.intentOf(ctx, name, row)
	if err != nil {
		writeActionError(w, err)
		return
	}
	def, _ := s.loaded().Registry.Get(dintent.Name(intent))
	after, err := s.newestTurn(ctx, u)
	if err != nil {
		writeActionError(w, err)
		return
	}
	payload, err := json.Marshal(actionPayload{Action: name, AfterTurn: after, ActionRequest: req})
	if err != nil {
		writeActionError(w, err)
		return
	}
	i, joined, err := s.intakes.Accept(ctx, intake.Request{Unit: u, Intent: intent, Source: source, Payload: payload, Guidance: req.Guidance, NoBrief: req.NoBrief,
		CanJoin: joinsAction(name, def, req.Inputs)})
	if err != nil {
		writeActionError(w, err)
		return
	}
	if i.State.Open() && i.State != intake.Starting {
		s.drive(i.Seq)
	}
	wi := s.wireIntake(ctx, i, 0, false)
	wi.Joined = joined
	writeJSON(w, api.ActionResult{OK: true, Intake: &wi})
}

// turnByHandIntake runs a turn by hand through an intake, waiting up to
// sessionWait for it to start, and for its start to end once it began; one
// not started by then stays open for the same session's next call to join,
// and is canceled when none comes.
func (s *Server) turnByHandIntake(w http.ResponseWriter, ctx context.Context, d dintent.Def, u unit.Unit, req api.ByHandRequest, handle string) {
	after, err := s.newestTurn(ctx, u)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	payload, err := json.Marshal(byHandPayload{Handle: handle, AfterTurn: after, ByHandRequest: req})
	if err != nil {
		writeAgentError(w, err)
		return
	}
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := time.AfterFunc(s.sessionWait, cancel)
	defer timer.Stop()
	i, err := s.acceptSession(ctx, intake.Request{Unit: u, Intent: turn.Intent(req.Intent), Source: sourceSession, Payload: payload, Guidance: req.Guidance,
		CanJoin: joinsSession(handle, d, req.Inputs)}, func() { timer.Stop() })
	if err != nil {
		writeAgentError(w, err)
		return
	}
	if i.State == intake.Starting {
		writeError(w, http.StatusConflict, fmt.Sprintf("the turn of %s is starting in the daemon (intake %d); run this command again in a minute", u.Label(), i.Seq))
		return
	}
	got, err := s.intakes.Drive(wctx, i.Seq)
	s.sessionAsks.end(i.Seq, s.deps.Now())
	if v, ok := s.byHand.LoadAndDelete(i.Seq); ok {
		s.sessionAsks.forget(i.Seq)
		out := v.(byHandOutcome)
		if out.err != nil {
			writeAgentError(w, out.err)
			return
		}
		writeJSON(w, out.res)
		return
	}
	if err != nil {
		if again, ok, lerr := s.deps.Intakes.Intake(ctx, i.Seq); lerr == nil && ok {
			got = again
		}
	}
	switch got.State {
	case intake.Failed:
		writeError(w, http.StatusInternalServerError, got.Error)
	case intake.Canceled:
		writeError(w, http.StatusConflict, fmt.Sprintf("intake %d was canceled", i.Seq))
	default:
		writeError(w, http.StatusConflict, notReady(u, s.wireIntake(ctx, got, 0, false)))
	}
}

// acceptSession accepts a turn by hand and marks it asked for under the
// gate the expiry holds, so the intake it joins cannot expire in between.
func (s *Server) acceptSession(ctx context.Context, r intake.Request, onStarting func()) (intake.Intake, error) {
	s.sessionAsks.gate.Lock()
	defer s.sessionAsks.gate.Unlock()
	if err := s.expireSessionIntakesLocked(ctx); err != nil {
		return intake.Intake{}, err
	}
	i, _, err := s.intakes.Accept(ctx, r)
	if err != nil || i.State == intake.Starting {
		return i, err
	}
	s.sessionAsks.begin(i.Seq, onStarting)
	return i, nil
}

func notReady(u unit.Unit, w api.Intake) string {
	wait := brief.WaitBriefing
	if !w.Next.IsZero() {
		wait = brief.WaitRetry
	}
	return fmt.Sprintf("the brief of %s is not ready yet (%s, attempt %d); run this command again in a minute", u.Label(), wait, max(w.Attempt, 1))
}

// wireIntake is i for the API, with its log lines past after when withLog;
// a waiting intake's attempt comes from the brief queue, its last error and
// next attempt from its pull request's brief record.
func (s *Server) wireIntake(ctx context.Context, i intake.Intake, after int, withLog bool) api.Intake {
	var rec dinv.BriefRecord
	found := false
	if briefing(i) && s.deps.Records != nil {
		var err error
		rec, found, err = s.deps.Records.BriefRecord(ctx, i.Unit)
		found = found && err == nil
	}
	w := s.intakeView(i, rec, found, s.deps.Now(), func() (int, error) { return s.deps.BriefViews.Attempt(ctx, refOf(i.Unit)) })
	if withLog {
		w.Log = slices.Clone(i.Log[min(max(after, 0), len(i.Log)):])
	}
	return w
}

// briefing tells an intake whose brief attempt and retry show.
func briefing(i intake.Intake) bool { return i.State.Open() && i.State != intake.Starting }

// intakeView is the wire intake without its log, from the pull request's
// brief record as of now; attempt asks the brief queue.
func (s *Server) intakeView(i intake.Intake, rec dinv.BriefRecord, found bool, now time.Time, attempt func() (int, error)) api.Intake {
	w := api.Intake{Seq: i.Seq, PR: refOf(i.Unit).URL(), Intent: string(i.Intent), Source: i.Source, State: string(i.State), Brief: i.Brief, Note: i.Note, Error: i.Error, LogLen: len(i.Log)}
	if !briefing(i) || s.deps.Records == nil {
		return w
	}
	w.Attempt = 1
	if found && rec.GaveUpAt.IsZero() {
		w.Attempt = rec.Failures + 1
		if s.deps.BriefViews != nil {
			if n, err := attempt(); err == nil {
				w.Attempt = n
			}
		}
		if rec.LastRun != 0 && rec.LastRun != rec.Run {
			w.LastError = rec.Error
		}
		if rec.RetryAt.After(now) {
			w.Next = rec.RetryAt
		}
	}
	return w
}

func intakeSeq(w http.ResponseWriter, r *http.Request) (int64, bool) {
	seq, err := strconv.ParseInt(r.PathValue("seq"), 10, 64)
	if err != nil || seq <= 0 {
		writeError(w, http.StatusBadRequest, "invalid intake number")
		return 0, false
	}
	return seq, true
}

func (s *Server) handleIntake(w http.ResponseWriter, r *http.Request) {
	seq, ok := intakeSeq(w, r)
	if !ok {
		return
	}
	if s.intakes == nil {
		writeError(w, http.StatusServiceUnavailable, errIntakesUnavailable.Error())
		return
	}
	q := r.URL.Query()
	after, wait := 0, 0
	for name, dst := range map[string]*int{"after": &after, "wait": &wait} {
		if v := q.Get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, name+" is a count")
				return
			}
			*dst = n
		}
	}
	wait = min(wait, api.IntakeWaitMax)
	woke, release := s.intakeWaiters.watch(seq)
	defer release()
	i, found, err := s.deps.Intakes.Intake(r.Context(), seq)
	if err == nil && found && len(i.Log) <= after && i.State.Open() && wait > 0 {
		select {
		case <-woke:
		case <-time.After(time.Duration(wait) * time.Second):
		case <-r.Context().Done():
			return
		}
		i, found, err = s.deps.Intakes.Intake(r.Context(), seq)
	}
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	case !found:
		writeError(w, http.StatusNotFound, fmt.Sprintf("no intake %d", seq))
	default:
		writeJSON(w, s.wireIntake(r.Context(), i, after, true))
	}
}

func (s *Server) handleIntakeWithout(w http.ResponseWriter, r *http.Request) {
	s.changeIntake(w, r, func(ctx context.Context, i intake.Intake) error {
		if i.Source == sourceSession {
			return conflict{catalog.SessionWithout(i.Seq)}
		}
		return s.intakes.Without(ctx, i.Seq)
	}, "intake %d starts without the brief")
}

func (s *Server) handleIntakeCancel(w http.ResponseWriter, r *http.Request) {
	s.changeIntake(w, r, func(ctx context.Context, i intake.Intake) error {
		if err := s.intakes.Cancel(ctx, i.Seq); err != nil {
			return err
		}
		s.sessionAsks.forget(i.Seq)
		return nil
	}, "intake %d canceled")
}

func (s *Server) changeIntake(w http.ResponseWriter, r *http.Request, change func(ctx context.Context, i intake.Intake) error, done string) {
	seq, ok := intakeSeq(w, r)
	if !ok {
		return
	}
	if s.intakes == nil {
		if s.refusePlugin(w, unit.Unit{Provider: unit.GitHub}) {
			return
		}
		writeError(w, http.StatusServiceUnavailable, errIntakesUnavailable.Error())
		return
	}
	ctx := context.WithoutCancel(r.Context())
	i, found, err := s.deps.Intakes.Intake(ctx, seq)
	if err == nil && !found {
		err = fmt.Errorf("intake %d: %w", seq, intake.ErrUnknown)
	}
	if err == nil && s.refusePlugin(w, i.Unit) {
		return
	}
	if err == nil {
		err = change(ctx, i)
	}
	var refused conflict
	switch {
	case errors.Is(err, intake.ErrUnknown):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, intake.ErrNotWaiting), errors.As(err, &refused):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if i.Source != sourceSession {
		if again, ok, err := s.deps.Intakes.Intake(ctx, seq); err == nil && ok && again.State.Open() {
			s.drive(seq)
		}
	}
	writeJSON(w, api.ActionResult{OK: true, Output: []string{fmt.Sprintf(done, seq)}})
}

// breakerWarning is the health warning while the brief breaker is open.
func breakerWarning(b api.BriefsHealth, now time.Time) string {
	when := brand.Name + " tries again at " + b.Next.Local().Format("15:04")
	if !b.Next.After(now) {
		when = "a trial runs now"
	}
	return fmt.Sprintf("briefs unavailable since %s: %s; %s", b.Since.Local().Format("15:04"), b.Error, when)
}

// BriefReady is the brief queue's answer for a round the sweep is about to
// propose or send on the pull request.
func (s *Server) BriefReady(ctx context.Context, u unit.Unit) (intake.Readiness, error) {
	first, err := intakeFirsts{s}.First(ctx, u)
	if err != nil {
		return intake.Readiness{}, err
	}
	return intakeBriefs(s.deps).Ready(ctx, u, first, nil)
}

// OpenIntakes lists the open intakes but the session ones nobody asks for;
// none when the daemon keeps no intakes.
func (s *Server) OpenIntakes(ctx context.Context) ([]intake.Intake, error) {
	if s.intakes == nil {
		return nil, nil
	}
	return s.openIntakes(ctx)
}
