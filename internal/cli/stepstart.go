package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/pr"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/turns"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/round"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/intent/builtin"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/plugins/source/value"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// The rounds of a pull request's plan, as the github planner names them.
const (
	roundFollowup = "follow-up"
	roundFirst    = "first"
	roundAddress  = "address"
)

// plannedRound is a round of the unit's plugin plan, with what the host
// resolves of it: its workspace, and the pull request as the host keeps
// what the plugin reported, for its turn.
type plannedRound struct {
	src.Round
	ws   wsdomain.Workspace
	read dinv.PlanFacts
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

// startOf is st's arguments and the manifest of the intent it starts on
// units of p: the holder of the named intent's role, which is a user's
// intent when one takes the role.
func (e *Env) startOf(st workflow.Step, p unit.ProviderID) startArgs {
	a := startArgs{}
	a.intent, _ = st.Args["intent"].(string)
	a.round, _ = st.Args["stage"].(string)
	a.runner, _ = st.Args["runner"].(string)
	if l := e.loadedIntents(); l != nil {
		a.def, _ = l.Registry.Get(dintent.Name(a.intent))
		if h, ok := l.Registry.ByRole(a.def.Role, p); a.def.Role != "" && ok {
			a.def, a.intent = h, string(h.Name)
		}
	}
	return a
}

func isTurnStart(st workflow.Step) bool { return st.Use == builtin.StepTurnStart }

// candidate is the sweep's round the intent's role's round kind and the step's
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
		return cmp.Or(p.planned.Reason, p.args.kindName())
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

// runnerName is the step's runner, else the intent's first runner.
func (a startArgs) runnerName() string {
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
	ref     pr.Ref
	args    startArgs
	facts   workflow.Facts
	planned plannedRound
	// round is a round of the unit's plan, sent through sendRound.
	round bool
	first bool
	// briefMissing is why the round goes without its brief; brief is the
	// readiness a refusal waits on.
	briefMissing string
	brief        intake.Readiness
}

func (p startPlan) observed() bool { return p.round && p.planned.Observed }

// address is why the plan's address round runs now; nil for other rounds.
func (p startPlan) address(now time.Time) *turn.RoundReason {
	if p.args.def.RoundKind != builtin.RoundKindAddress {
		return nil
	}
	return automatedReason(withTurnFacts(p.facts, p.planned.TurnFacts, now), p.planned.read.Facts)
}

// withTurnFacts are f with the turn slot facts of a planned round over them.
func withTurnFacts(f workflow.Facts, turn []src.Fact, now time.Time) workflow.Facts {
	if len(turn) == 0 {
		return f
	}
	out := maps.Clone(f)
	if out == nil {
		out = workflow.Facts{}
	}
	for _, t := range turn {
		out[t.Name] = value.Resolve(t.Value, now)
	}
	return out
}

// plan is the round named name of u's plugin plan, over u's round state:
// the bound one while a sweep runs, else the stored one; false when the plan
// has no such round. A round the plan holds as not due is only skipped.
func (s *stepSet) plan(ctx context.Context, u unit.Unit, name string) (plannedRound, bool, error) {
	pl := s.d.Planners[u.Provider]
	if pl == nil {
		return plannedRound{}, false, fmt.Errorf("no plugin plans the rounds of %s", u.Provider)
	}
	st := s.bound()
	if st == nil {
		var err error
		if st, err = s.rounds(ctx, false); err != nil {
			return plannedRound{}, false, err
		}
	}
	rs, err := s.e.roundStateOf(ctx, u, st, s.d.Overrides)
	if err != nil {
		return plannedRound{}, false, err
	}
	plan, err := pl.Plan(ctx, psource.Wire(u), rs)
	if err != nil {
		return plannedRound{}, false, err
	}
	i := slices.IndexFunc(plan.Rounds, func(r src.Round) bool { return r.Name == name })
	if i < 0 {
		return plannedRound{}, false, nil
	}
	out := plannedRound{Round: plan.Rounds[i], ws: s.workspaceOf(ctx, plan.Rounds[i].Workspace)}
	if out.read, err = s.e.keptPR(ctx, u); err != nil {
		return plannedRound{}, false, err
	}
	return out, true, nil
}

// workspaceOf is the workspace ref names, as its manager lists it; a
// workspace no manager lists now keeps its name.
func (s *stepSet) workspaceOf(ctx context.Context, ref src.WorkspaceRef) wsdomain.Workspace {
	out := wsdomain.Workspace{Manager: ref.Manager, ID: ref.ID}
	if ref.ID == "" || s.e.WS == nil {
		return out
	}
	if ws, ok, err := s.e.WS.Find(ctx, ref.ID); err == nil && ok {
		return ws
	}
	return out
}

func (s *stepSet) startCheck(ctx context.Context, c stepCall, o automation.CheckOpts) (string, error) {
	refusal, _, err := s.startGuards(ctx, c, o.ByHand, false)
	return refusal, err
}

// startGuards are the base's checks of a round, in its order. live, under
// the loop lock, asks the brief queue for the brief and counts the caps in
// the bound round state; otherwise it starts nothing and writes nothing.
func (s *stepSet) startGuards(ctx context.Context, c stepCall, byHand, live bool) (string, startPlan, error) {
	p := startPlan{args: s.e.startOf(c.st, c.u.Provider)}
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
		planned, ok, err := s.plan(ctx, c.u, r)
		if err != nil {
			return "", p, err
		}
		if !ok || planned.NotDue != "" {
			return "its " + p.args.kindName() + " is not due", p, nil
		}
		p.planned, p.round = planned, true
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
	if p.planned.Observed || p.planned.ws.ID == "" || s.e.WS == nil {
		return ""
	}
	activity, err := s.e.WS.AgentActivity(ctx)
	if err != nil {
		return ""
	}
	a := activity[p.planned.ws.ID]
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
// the round would replace the turn the agent finishes with the answer,
// unless its role replaces a question its head moved past.
func (s *stepSet) asks(ctx context.Context, u unit.Unit, p *startPlan) (string, error) {
	if s.e.Turns == nil {
		return "", nil
	}
	books, err := s.e.Turns.Store.Books(ctx, u)
	if err != nil {
		return "", err
	}
	b := books[u]
	p.first = !slices.ContainsFunc(b.Turns, func(t dturn.Turn) bool { return t.State != dturn.Withdrawn })
	if s.e.Rec == nil || !s.e.Rec.OpenQuestions(books)[p.ref][p.args.intent] {
		return "", nil
	}
	if role, _ := builtin.RoleOf(p.args.def.Role); p.round && role.ReplacesStaleQuestion && b.QuestionMovedPast(dturn.Intent(p.args.intent), p.planned.Head) {
		return "", nil
	}
	return "its " + p.args.intent + " turn asks you something", nil
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
func (s *stepSet) startBlocked(ctx context.Context, ref pr.Ref) (string, error) {
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

func isCapRefusal(refusal string) bool { return strings.HasPrefix(refusal, round.CapReached) }

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
	head := p.planned.Head
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
	args := s.e.startOf(c.st, c.u.Provider)
	planned, ok, err := s.plan(ctx, c.u, args.candidate())
	if err != nil || !ok || planned.NotDue != "" || planned.Observed {
		return err
	}
	if !st.FirstCapHit(ref.String(), s.e.Now()) {
		return nil
	}
	var errs []error
	if s.e.Rec != nil {
		errs = append(errs, s.e.Rec.Record(ctx, dinv.Event{PR: ref, Kind: dinv.KindRoundHeld, At: s.e.Now(), Data: why}))
	}
	fmt.Fprintf(s.e.Err, "%s held: %s %s, %s; raise the cap in %s or run '"+brand.Command+" %s %s'\n",
		s.e.stamp(), args.kindName(), ref, why, s.e.ConfigPath, args.intent, ref.URL())
	if planned.ws.ID != "" {
		errs = append(errs, s.e.setCard(ctx, planned.ws, "", true))
	}
	errs = append(errs, s.openHeldGate(ctx, c.u))
	return errors.Join(errs...)
}

// openHeldGate opens the plan gate of u's round the daily cap held today, once: approving it runs the round by
// hand, and no rule releases it, since the cap held it.
func (s *stepSet) openHeldGate(ctx context.Context, u unit.Unit) error {
	if s.d.Gates == nil {
		return nil
	}
	subject := round.HeldSubject(s.e.Now())
	open, err := s.d.Gates.Where(ctx, gates.Query{Kind: gate.KindPlan, Unit: u})
	if err != nil || slices.ContainsFunc(open, func(g gate.Gate) bool { return g.Subject == subject }) {
		return err
	}
	_, err = s.d.Gates.Open(ctx, gate.Gate{Kind: gate.KindPlan, Unit: u, Subject: subject}, waitsForYou{})
	return err
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
		started, err := s.startTurn(ctx, c.u, p.args, guidance, &why)
		if err != nil {
			if taken {
				s.e.keepGuidance(ctx, p.ref, guidance)
			}
			return "", startError(err)
		}
		st.RecordSent(p.ref.String(), "", "", s.e.Now())
		return started, nil
	}
	reason := s.reason(p)
	sent, err := s.e.sendRound(ctx, st, roundCall{Kind: p.args.kind(), Workspace: p.planned.ws, Unit: c.u, Facts: p.planned.read,
		Reason: reason, Head: p.planned.Head, OnSent: p.planned.OnSent, OnSkipped: p.planned.OnSkipped, Guidance: guidance, BriefMissing: p.briefMissing, Observed: p.planned.Observed,
		Confirmed: o.Confirmed, Why: &why, Address: p.address(s.e.Now())})
	if !sent && guidance != "" {
		s.e.keepGuidance(ctx, p.ref, guidance)
	}
	switch {
	case err != nil:
		return "", startError(err)
	case p.planned.Observed:
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
	case errors.Is(err, workspace.ErrUnreachable), errors.Is(err, src.ErrUnavailable), errors.Is(err, dgh.ErrBudget):
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
	args := s.e.startOf(c.st, c.u.Provider)
	if r := args.candidate(); r != "" {
		planned, ok, err := s.plan(ctx, c.u, r)
		if err != nil {
			return err
		}
		if ok {
			setMarks(st, ref.String(), planned.OnSkipped)
		}
	}
	if s.e.Rec == nil {
		return nil
	}
	return s.e.Rec.Record(ctx, dinv.Event{PR: ref, Kind: dinv.KindRoundSkipped, At: s.e.Now(), Data: args.kindName() + ": skipped by you"})
}
