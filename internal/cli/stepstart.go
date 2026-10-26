package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/intake"
	coresweep "github.com/wloophq/wloop/internal/core/sweep"
	"github.com/wloophq/wloop/internal/core/turns"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/round"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace"
)

// The rounds a roundCandidate names, as the github facts provider does.
const (
	roundFollowup = "follow-up"
	roundFirst    = "first"
	roundAddress  = "address"
)

// roundCandidate is the round of a pull request the github facts plan, with
// what its turn is sent with.
type roundCandidate struct {
	Round        string
	Workspace    wsdomain.Workspace
	Facts        coresweep.Facts
	Fingerprint  string
	ChecksHead   string
	ConflictHead string
	LastReviewAt time.Time
	Observed     bool
	// Reason is why the planner plans the round.
	Reason string
}

// roundCandidates is the github facts provider's Candidate: false when the
// round is not due.
type roundCandidates interface {
	Candidate(ctx context.Context, u unit.Unit, round string) (roundCandidate, bool, error)
}

// refusalBriefNotReady starts the refusal of a round whose brief is not
// ready.
const refusalBriefNotReady = "brief not ready"

// startArgs are a turn.start step's intent, round and runner, with the
// intent's manifest; def is zero when the intent does not load.
type startArgs struct {
	intent, round, runner string
	def                   dintent.Def
}

// startOf is st's arguments and the manifest of its intent.
func (e *Env) startOf(st workflow.Step) startArgs {
	a := startArgs{}
	a.intent, _ = st.Args["intent"].(string)
	a.round, _ = st.Args["stage"].(string)
	a.runner, _ = st.Args["runner"].(string)
	if l := e.loadedIntents(); l != nil {
		a.def, _ = l.Registry.Get(dintent.Name(a.intent))
	}
	return a
}

func isTurnStart(st workflow.Step) bool { return st.Use == builtin.StepTurnStart }

// candidate is the sweep's round the intent's round_kind and the step's
// round start, "" for an intent that sends none.
func (a startArgs) candidate() string {
	switch a.def.RoundKind {
	case builtin.RoundKindReview:
		if a.round == roundFirst {
			return roundFirst
		}
		return roundFollowup
	case builtin.RoundKindAddress:
		return roundAddress
	}
	return ""
}

func (a startArgs) kind() stepKind {
	switch a.candidate() {
	case roundFirst:
		return stepFirstRound
	case roundAddress:
		return stepAddress
	}
	return stepFollowup
}

// kindName names the round as the pending rounds name it.
func (a startArgs) kindName() string {
	if a.candidate() == "" {
		return a.intent
	}
	return step{kind: a.kind()}.roundKind()
}

// reason is why the round runs: the first round's planner says it, any
// other round the intent's why over the unit's facts.
func (s *stepSet) reason(p startPlan) string {
	if p.args.candidate() == roundFirst {
		return cmp.Or(p.cand.Reason, p.args.kindName())
	}
	var why string
	if l := s.e.loadedIntents(); l != nil {
		var err error
		if why, err = l.Why(p.args.def.Name, p.facts); err != nil {
			s.logf("%s: the why of %s: %v", p.ref, p.args.intent, err)
		}
	}
	return cmp.Or(why, p.args.kindName())
}

// mode is the step's mode, else the intent's first mode.
func (a startArgs) runMode() string {
	if a.runner != "" {
		return a.runner
	}
	if len(a.def.Runners) > 0 {
		return a.def.Runners[0]
	}
	return string(dturn.RunnerSession)
}

// startPlan is what the guards of a turn.start read, for its run.
type startPlan struct {
	ref   github.Ref
	args  startArgs
	facts workflow.Facts
	cand  roundCandidate
	// round is a round of the sweep's, sent through sendRound.
	round bool
	first bool
	// briefMissing is why the round goes without its brief; brief is the
	// readiness a refusal waits on.
	briefMissing string
	brief        intake.Readiness
}

func (p startPlan) observed() bool { return p.round && p.cand.Observed }

// address is why the plan's address round runs now; nil for other rounds.
func (p startPlan) address() *turn.RoundReason {
	if p.args.def.RoundKind != builtin.RoundKindAddress {
		return nil
	}
	return automatedReason(p.facts, p.cand.Facts.Facts)
}

func (s *stepSet) candidate(ctx context.Context, u unit.Unit, r string) (roundCandidate, bool, error) {
	if s.d.Candidates == nil {
		return roundCandidate{}, false, errors.New("no github facts provider plans the rounds")
	}
	return s.d.Candidates.Candidate(ctx, u, r)
}

func (s *stepSet) startCheck(ctx context.Context, c stepCall, o automation.CheckOpts) (string, error) {
	refusal, _, err := s.startGuards(ctx, c, o.ByHand, false)
	return refusal, err
}

// startGuards are the base's checks of a round, in its order. live, under
// the loop lock, asks the brief queue for the brief and counts the caps in
// the bound round state; otherwise it starts nothing and writes nothing.
func (s *stepSet) startGuards(ctx context.Context, c stepCall, byHand, live bool) (string, startPlan, error) {
	p := startPlan{args: s.e.startOf(c.st)}
	if !slices.Contains(builtin.RoundProviders, c.u.Provider) {
		return s.unitGuards(ctx, c, p)
	}
	ref, err := githubRef(c.u)
	if err != nil {
		return "", p, err
	}
	p.ref = ref
	if p.facts, err = s.facts(ctx, c.u); err != nil {
		return "", p, err
	}
	if factBool(p.facts, "pr.held") {
		return "held by you", p, nil
	}
	if byHand && s.d.Rounds != nil {
		pause, err := s.d.Rounds.Pause(ctx)
		if err != nil {
			return "", p, err
		}
		if pause.Holds(ref.Owner, ref.Repo) {
			return whyPaused, p, nil
		}
	}
	if factBool(p.facts, "pr.stale") {
		return "its stored detail is stale", p, nil
	}
	if r := p.args.candidate(); r != "" {
		cand, ok, err := s.candidate(ctx, c.u, r)
		if err != nil {
			return "", p, err
		}
		if !ok {
			return "its " + p.args.kindName() + " is not due", p, nil
		}
		p.cand, p.round = cand, true
		if why := s.activity(ctx, p, byHand); why != "" {
			return why, p, nil
		}
	}
	if why, err := s.asks(ctx, c.u, &p); err != nil || why != "" {
		return why, p, err
	}
	if !p.observed() {
		if why, err := s.intakeAsks(ctx, c.u, p.args.intent); err != nil || why != "" {
			return why, p, err
		}
	}
	if why, err := s.startBlocked(ctx, ref); err != nil || why != "" {
		return why, p, err
	}
	if !p.observed() {
		if why := s.briefGate(ctx, c.u, &p, live); why != "" {
			return why, p, nil
		}
	}
	if why, err := s.refusal(p); err != nil || why != "" {
		return why, p, err
	}
	if !(p.observed() && p.args.kind() == stepFollowup) {
		st := s.bound()
		if !live {
			if st, err = s.rounds(ctx, byHand); err != nil {
				return "", p, err
			}
		}
		if st == nil {
			return "", p, errNoRoundState
		}
		cfg, err := s.e.loadConfig()
		if err != nil {
			return "", p, err
		}
		if why := st.CapReason(ref.String(), s.e.Now(), cfg.Turns.PerUnitPerDay); why != "" {
			return why, p, nil
		}
	}
	return "", p, nil
}

// activity refuses a round its workspace cannot take now: for address an
// agent waiting on a question; for a review an agent working, or the user
// in the workspace unless the user applies it.
func (s *stepSet) activity(ctx context.Context, p startPlan, byHand bool) string {
	if p.cand.Observed || p.cand.Workspace.ID == "" || s.e.WS == nil {
		return ""
	}
	activity, err := s.e.WS.AgentActivity(ctx)
	if err != nil {
		return ""
	}
	a := activity[p.cand.Workspace.ID]
	switch {
	case p.args.kind() == stepAddress:
		if a.Waiting {
			return errWaiting.Error()
		}
	case a.Busy:
		return errBusy.Error()
	case a.UserActive && !byHand:
		return errUserActive.Error()
	}
	return ""
}

// asks refuses a round whose intent's newest turn asks the user something:
// the round would replace the turn the agent finishes with the answer.
func (s *stepSet) asks(ctx context.Context, u unit.Unit, p *startPlan) (string, error) {
	if s.e.Turns == nil {
		return "", nil
	}
	books, err := s.e.Turns.Store.Books(ctx, u)
	if err != nil {
		return "", err
	}
	p.first = !slices.ContainsFunc(books[u].Turns, func(t dturn.Turn) bool { return t.State != dturn.Withdrawn })
	if s.e.Rec != nil && s.e.Rec.OpenQuestions(books)[p.ref][p.args.intent] {
		return "its " + p.args.intent + " turn asks you something", nil
	}
	return "", nil
}

func (s *stepSet) intakeAsks(ctx context.Context, u unit.Unit, intent string) (string, error) {
	if s.e.Briefing == nil {
		return "", nil
	}
	open, err := s.e.Briefing.OpenIntakes(ctx)
	if err != nil {
		return "", err
	}
	if slices.ContainsFunc(open, func(i intake.Intake) bool { return i.Unit == u && string(i.Intent) == intent }) {
		return "an intake of yours asks for it", nil
	}
	return "", nil
}

// startBlocked refuses a round its start block holds: folder trust is the
// user's alone, so no step clears it.
func (s *stepSet) startBlocked(ctx context.Context, ref github.Ref) (string, error) {
	if s.e.Rec == nil {
		return "", nil
	}
	blocks, err := s.e.Rec.FirstRoundBlocks(ctx)
	if err != nil {
		return "", err
	}
	ev, ok := blocks[ref.Unit()]
	if !ok {
		return "", nil
	}
	b, ok := inventory.StartBlockOf(ev)
	if !ok {
		return "", nil
	}
	return "round blocked: " + cmp.Or(ev.Data, b.Question), nil
}

// briefGate refuses a round whose brief is not ready, peeking unless live.
// A brief that cannot be read lets the round go without it.
func (s *stepSet) briefGate(ctx context.Context, u unit.Unit, p *startPlan, live bool) string {
	if s.d.Briefs == nil {
		return ""
	}
	ask := s.d.Briefs.Peek
	if live {
		ask = s.d.Briefs.Ready
	}
	rd, err := ask(ctx, u, p.first, p.args.def.Brief.Need)
	switch {
	case err != nil:
		p.briefMissing = "reading it failed (" + err.Error() + ")"
	case !rd.Go:
		p.brief = rd
		return refusalBriefNotReady + " (" + waitText(rd) + ")"
	case rd.Without:
		p.briefMissing = rd.Note
	}
	return ""
}

func waitText(rd intake.Readiness) string {
	return fmt.Sprintf("%s, attempt %d", rd.Wait, rd.Attempt)
}

// refusal is the why of the intent's first [[refuse]] whose when holds
// over the unit's facts, "" for none.
func (s *stepSet) refusal(p startPlan) (string, error) {
	l := s.e.loadedIntents()
	if l == nil {
		return "", nil
	}
	return l.Refusal(p.args.def.Name, p.facts)
}

func isCapRefusal(refusal string) bool { return strings.Contains(refusal, "daily cap reached") }

// startNote is what the sweep wrote on a round it held: a first round
// waiting for its brief starts it and says so; a cap's first hit of the day
// is announced, recorded and marks the workspace unread.
func (s *stepSet) startNote(ctx context.Context, c stepCall, refusal string) error {
	switch {
	case strings.HasPrefix(refusal, refusalBriefNotReady):
		if s.d.Briefs == nil {
			return nil
		}
		why, p, err := s.startGuards(ctx, c, false, true)
		if err != nil || !strings.HasPrefix(why, refusalBriefNotReady) {
			return err
		}
		return s.recordBriefWaiting(ctx, p)
	case isCapRefusal(refusal):
		return s.holdByCap(ctx, c, refusal)
	}
	return nil
}

// kindBriefWaiting records once per wait, keyed by intent and head, that a
// round waits for its brief; it has no situation meaning.
const kindBriefWaiting = "brief_waiting"

func (s *stepSet) recordBriefWaiting(ctx context.Context, p startPlan) error {
	head := p.cand.Facts.HeadRefOid
	if s.e.Rec == nil || head == "" {
		return nil
	}
	ev := dinv.Event{PR: p.ref, Kind: kindBriefWaiting, Key: p.args.intent + ":" + head, At: s.e.Now(), Data: waitText(p.brief)}
	return s.e.Rec.Record(ctx, ev)
}

func (s *stepSet) holdByCap(ctx context.Context, c stepCall, why string) error {
	st := s.bound()
	if st == nil {
		return errNoRoundState
	}
	ref, err := githubRef(c.u)
	if err != nil {
		return err
	}
	args := s.e.startOf(c.st)
	cand, _, err := s.candidate(ctx, c.u, args.candidate())
	if err != nil || cand.Observed {
		return err
	}
	if !st.FirstCapHit(ref.String(), s.e.Now()) {
		return nil
	}
	var errs []error
	if s.e.Rec != nil {
		errs = append(errs, s.e.Rec.Record(ctx, dinv.Event{PR: ref, Kind: dinv.KindRoundHeld, At: s.e.Now(), Data: why}))
	}
	fmt.Fprintf(s.e.Err, "%s held: %s %s, %s; raise the cap in %s or run '"+brand.Command+" review %s'\n",
		s.e.stamp(), args.kindName(), ref, why, s.e.ConfigPath, ref.URL())
	if cand.Workspace.ID != "" {
		errs = append(errs, s.e.setCard(ctx, cand.Workspace, "", true))
	}
	return errors.Join(errs...)
}

// startRun checks again on current facts, then sends the round through
// sendRound in the bound round state, or launches a turn of an intent the
// sweep sends no round of. The trigger stays automated, applied by hand
// or not.
func (s *stepSet) startRun(ctx context.Context, c stepCall, o automation.RunOpts) (string, error) {
	if !slices.Contains(builtin.RoundProviders, c.u.Provider) {
		return s.unitStart(ctx, c, o)
	}
	st := s.bound()
	if st == nil {
		return "", errNoRoundState
	}
	refusal, p, err := s.startGuards(ctx, c, o.ByHand, true)
	if err != nil {
		return "", err
	}
	if refusal != "" {
		if strings.HasPrefix(refusal, refusalBriefNotReady) {
			if err := s.recordBriefWaiting(ctx, p); err != nil {
				s.logf("%s: recording %s: %v", p.ref, kindBriefWaiting, err)
			}
		}
		return "", automation.Refused{Reason: refusal}
	}
	guidance, taken := o.Guidance, false
	if guidance == "" && s.e.Rec != nil && !p.observed() {
		if guidance, err = s.e.Rec.TakeNextGuidance(ctx, p.ref); err != nil {
			return "", err
		}
		taken = guidance != ""
	}
	why := c.why
	if !p.round {
		err := cliLauncher{s.e}.Launch(ctx, turns.Launch{Unit: c.u, Intent: dturn.Intent(p.args.intent), Guidance: guidance, Trigger: dturn.Automated, Why: &why})
		if err != nil {
			if taken {
				s.e.keepGuidance(ctx, p.ref, guidance)
			}
			return "", startError(err)
		}
		st.RecordFollowup(p.ref.String(), markOf(p.args.kind()), "", s.e.Now())
		return "started a " + p.args.intent + " turn", nil
	}
	reason := s.reason(p)
	sent, err := s.e.sendRound(ctx, st, roundCall{Kind: p.args.kind(), Workspace: p.cand.Workspace, Unit: c.u, Facts: p.cand.Facts,
		Reason: reason, Fingerprint: p.cand.Fingerprint, ChecksHead: p.cand.ChecksHead, ConflictHead: p.cand.ConflictHead, LastReviewAt: p.cand.LastReviewAt,
		Guidance: guidance, BriefMissing: p.briefMissing, Observed: p.cand.Observed, Confirmed: o.Confirmed, Why: &why, Address: p.address()})
	if !sent && guidance != "" {
		s.e.keepGuidance(ctx, p.ref, guidance)
	}
	switch {
	case err != nil:
		return "", startError(err)
	case p.cand.Observed:
		return "observed the " + p.args.kindName() + ": " + reason, nil
	case !sent:
		return "", automation.Transient{Err: errors.New("its submission was not observed")}
	}
	return "sent the " + p.args.kindName() + ": " + reason, nil
}

// startError is a send's failure as the engine takes it: the workspace not
// free now or a start block refuse, an unreachable manager or GitHub is
// tried again, anything else fails.
func startError(err error) error {
	var untrusted *turns.UntrustedError
	var asked *turns.QuestionError
	switch {
	case errors.Is(err, errBusy), errors.Is(err, errUserActive), errors.Is(err, errWaiting):
		return automation.Refused{Reason: err.Error()}
	case errors.As(err, &untrusted), errors.As(err, &asked):
		return automation.Refused{Reason: "round blocked: " + err.Error()}
	case errors.Is(err, workspace.ErrUnreachable), errors.Is(err, github.ErrRateLimited), errors.Is(err, github.ErrUnavailable), errors.Is(err, dgh.ErrBudget):
		return automation.Transient{Err: err}
	}
	return err
}

// startDismiss writes what skipping the round wrote: handled until
// something new happens on the pull request.
func (s *stepSet) startDismiss(ctx context.Context, c stepCall) error {
	if !slices.Contains(builtin.RoundProviders, c.u.Provider) {
		return nil
	}
	st := s.bound()
	if st == nil {
		return errNoRoundState
	}
	ref, err := githubRef(c.u)
	if err != nil {
		return err
	}
	args := s.e.startOf(c.st)
	if r := args.candidate(); r != "" {
		cand, ok, err := s.candidate(ctx, c.u, r)
		if err != nil {
			return err
		}
		skip(st, ref.String(), args.kind(), cand, ok)
	}
	if s.e.Rec == nil {
		return nil
	}
	return s.e.Rec.Record(ctx, dinv.Event{PR: ref, Kind: dinv.KindRoundSkipped, At: s.e.Now(), Data: args.kindName() + ": skipped by you"})
}

// skip is sweep.skip on a candidate: a round the user skipped is not
// planned again until something new happens.
func skip(st *round.Store, key string, kind stepKind, cand roundCandidate, ok bool) {
	switch {
	case kind == stepFirstRound:
		st.SkipFirstRound(key)
	case !ok:
	case kind == stepAddress:
		if cand.Fingerprint != "" {
			st.SetMark(key, round.AddressedReviews, cand.Fingerprint)
		}
		st.SetHeads(key, cand.ChecksHead, cand.ConflictHead)
	default:
		st.SetMark(key, round.Reviewed, cand.Fingerprint)
	}
}
