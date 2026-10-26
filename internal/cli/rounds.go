package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	coresweep "github.com/wloophq/wloop/internal/core/sweep"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
)

// stepKind is the kind of round a turn.start step sends.
type stepKind int

const (
	stepFollowup stepKind = iota + 1
	stepFirstRound
	stepAddress
)

// step is one round to send.
type step struct {
	kind   stepKind
	ws     wsdomain.Workspace
	pr     github.Ref
	reason string
	// observed is a round the facts only log: its workspace is gone and its
	// project does not let the round recreate it.
	observed bool

	fingerprint string
	// checksHead is the head whose failing checks a triage round covers.
	checksHead string
	// conflictHead is the head whose conflict with the base a triage round
	// resolves.
	conflictHead string
	lastReviewAt time.Time
	read         *github.PullRequest // the pull request as the facts read it, for the turn
	// briefMissing is why the round goes without its brief, "" with one.
	briefMissing string
	// confirmed is a round the user sent from the plan.
	confirmed bool
	// fresh is a round whose pull request GitHub showed open and unmoved:
	// read stands for it.
	fresh bool
	// why is the workflow that started the round.
	why *workflow.Why
	// address is why an address round runs now, nil on other rounds.
	address *turn.RoundReason
}

func (s step) key() string { return s.pr.String() }

// payload is the pull request as the facts read it, nil when they did not.
func (s step) payload() turn.Payload {
	if s.read == nil {
		return nil
	}
	p := turnPR(s.read)
	p.Address = s.address
	return p
}

// mark is the round mark the step's reviews fingerprint sets.
func (s step) mark() string { return markOf(s.kind) }

func markOf(k stepKind) string {
	if k == stepAddress {
		return round.AddressedReviews
	}
	return round.Reviewed
}

func (s step) roundKind() string {
	switch s.kind {
	case stepFirstRound:
		return "first round"
	case stepAddress:
		return "triage"
	}
	return "follow-up"
}

// blockedIntent is the intent of the round b holds.
func (e *Env) blockedIntent(b inventory.StartBlock) dintent.Name {
	if b.Round == (step{kind: stepAddress}).roundKind() {
		return e.roleIntent(builtin.RoleAddress)
	}
	return e.roleIntent(builtin.RoleReview)
}

// roleIntent is the intent holding role on pull requests; "" when none
// loads.
func (e *Env) roleIntent(role string) dintent.Name {
	d, _ := e.loadedIntents().Registry.ByRole(role, unit.GitHub)
	return d.Name
}

func (s step) describe() string {
	switch s.kind {
	case stepFirstRound:
		return fmt.Sprintf("first round %s: %s", s.pr, s.reason)
	case stepAddress:
		switch {
		case s.observed:
			return fmt.Sprintf("triage %s without a workspace, observed: %s", s.pr, s.reason)
		case s.ws.ID == "":
			return fmt.Sprintf("triage %s without a workspace: %s", s.pr, s.reason)
		}
		return fmt.Sprintf("triage %s (%s): %s", s.ws.DisplayName, s.pr, s.reason)
	}
	switch {
	case s.observed:
		return fmt.Sprintf("follow-up %s without a workspace, observed: %s", s.pr, s.reason)
	case s.ws.ID == "":
		return fmt.Sprintf("follow-up %s without a workspace: %s", s.pr, s.reason)
	}
	return fmt.Sprintf("follow-up %s (%s): %s", s.ws.DisplayName, s.pr, s.reason)
}

// sweep sends rounds against one loaded round state.
type sweep struct {
	e   *Env
	cfg config.Config
	st  *round.Store
	now time.Time
}

func (s *sweep) logf(format string, args ...any) {
	fmt.Fprintf(s.e.Out, "%s %s\n", s.e.stamp(), fmt.Sprintf(format, args...))
}

func (s *sweep) warnf(format string, args ...any) {
	fmt.Fprintf(s.e.Err, "%s error: %s\n", s.e.stamp(), fmt.Sprintf(format, args...))
}

// whyPaused is why a pause drops a pending round.
const whyPaused = "automatic rounds paused"

// blockRound records a block of the round of kind with key, "" or a
// StartBlock's. Without an inventory it logs the reason once a day.
func (s *sweep) blockRound(ctx context.Context, ref github.Ref, kind, why, key string) {
	line := fmt.Sprintf("skip %s: my review was requested, but %s", ref, why)
	if kind != "first round" {
		line = fmt.Sprintf("skip the %s of %s: %s", kind, ref, why)
	}
	if s.e.Rec == nil {
		if s.st.FirstNote(line, s.now) {
			s.logf("%s", line)
		}
		return
	}
	ev := port.Event{Unit: ref.Unit(), Kind: dinv.KindRoundBlocked, Key: key, At: s.now, Data: why}
	if err := s.e.Rec.RecordUnit(ctx, ev); err != nil {
		s.warnf("%s: recording the blocked %s: %v; the next sweep retries", ref, kind, err)
		return
	}
	s.logf("%s", line)
}

func (s *sweep) record(ctx context.Context, st step, kind, data string) {
	if s.e.Rec == nil {
		return
	}
	ev := dinv.Event{PR: st.pr, Kind: kind, At: s.now, Data: data}
	if err := s.e.Rec.Record(ctx, ev); err != nil {
		s.warnf("%s: recording %s: %v", st.pr, kind, err)
	}
}

// skip records the round as handled without sending it, so the sweep plans
// none until something new happens on the pull request. It does not count
// against the caps.
func (s *sweep) skip(st step) {
	key := st.key()
	switch st.kind {
	case stepFirstRound:
		s.st.SkipFirstRound(key)
	case stepAddress:
		if st.fingerprint != "" {
			s.st.SetMark(key, round.AddressedReviews, st.fingerprint)
		}
		s.st.SetHeads(key, st.checksHead, st.conflictHead)
	default:
		s.st.SetMark(key, round.Reviewed, st.fingerprint)
	}
}

// withState runs fn against the round state, loaded and saved around it.
func (e *Env) withState(ctx context.Context, fn func(s *sweep) error) error {
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	rounds, err := e.rounds()
	if err != nil {
		return err
	}
	store, err := rounds.Load(ctx)
	if err != nil {
		return err
	}
	s := &sweep{e: e, cfg: cfg, st: store, now: e.Now()}
	if err := fn(s); err != nil {
		return err
	}
	return rounds.Save(ctx, store)
}

// sweepOptions are the session options of the rounds of intent, as its
// manifest declares them.
func (e *Env) sweepOptions(intent dintent.Name, opts roundOptions) roundOptions {
	d, _ := e.loadedIntents().Registry.Get(intent)
	return intentOptions(d, opts)
}

// noteStartProblem makes a round that cannot start until the user answers
// the agent's question visible. A round on an untrusted folder, or one that
// creates its workspace, stays blocked, so no sweep starts it again until
// the folder is trusted or the user acts; any other records why it was
// skipped, once per distinct reason.
func (s *sweep) noteStartProblem(ctx context.Context, st step, err error) {
	var block inventory.StartBlock
	var untrusted *turns.UntrustedError
	var asked *turns.QuestionError
	why := strings.TrimPrefix(fmt.Sprint(err), st.ws.DisplayName+": ")
	switch {
	case errors.As(err, &untrusted):
		block = inventory.StartBlock{Question: turns.QuestionTrust, Root: untrusted.Root, Folder: untrusted.Folder, Home: untrusted.Home,
			Homes: s.e.trustHomes(s.prConfig(st), untrusted.Home)}
		why = untrustedWhy(untrusted)
	case errors.As(err, &asked):
		block = inventory.StartBlock{Question: asked.Question, Root: asked.Root}
	default:
		return
	}
	if s.e.Rec == nil {
		return
	}
	if untrusted != nil || st.kind == stepFirstRound || st.ws.ID == "" {
		block.Round, block.Fingerprint, block.ChecksHead, block.ConflictHead = st.roundKind(), st.fingerprint, st.checksHead, st.conflictHead
		s.blockRound(ctx, st.pr, st.roundKind(), why, block.Key(s.now))
		return
	}
	sum := sha256.Sum256([]byte(st.roundKind() + "\n" + block.Key(time.Time{})))
	ev := dinv.Event{PR: st.pr, Kind: dinv.KindRoundSkipped, Key: "start " + hex.EncodeToString(sum[:8]), At: s.now, Data: st.roundKind() + ": " + why}
	if err := s.e.Rec.Record(ctx, ev); err != nil {
		s.warnf("%s: recording %s: %v", st.pr, dinv.KindRoundSkipped, err)
	}
}

// untrustedWhy is the outcome a round blocked on an untrusted folder shows.
func untrustedWhy(u *turns.UntrustedError) string {
	home := "the agent home of its terminal"
	if u.Home != "" {
		home = u.Home
	}
	return "could not start: " + u.Folder + " is not trusted in " + home
}

// roundCall is a round a workflow's turn.start sends.
type roundCall struct {
	Kind         stepKind
	Workspace    wsdomain.Workspace
	Unit         unit.Unit
	Facts        coresweep.Facts
	Reason       string
	Fingerprint  string
	ChecksHead   string
	ConflictHead string
	LastReviewAt time.Time
	Guidance     string
	// BriefMissing is why the round goes without its brief, "" with one.
	BriefMissing string
	// Observed is a round the facts only log: its workspace is gone and its
	// project does not let the round recreate it.
	Observed  bool
	Confirmed bool
	Why       *workflow.Why
	Address   *turn.RoundReason
}

// sendRound sends c's round with st as the round state, through send, so a
// round that cannot start records why as the sweep's do.
func (e *Env) sendRound(ctx context.Context, st *round.Store, c roundCall) (sent bool, err error) {
	cfg, err := e.loadConfig()
	if err != nil {
		return false, err
	}
	ref, err := githubRef(c.Unit)
	if err != nil {
		return false, err
	}
	s := &sweep{e: e, cfg: cfg, st: st, now: e.Now()}
	return s.send(ctx, step{kind: c.Kind, ws: c.Workspace, pr: ref, reason: c.Reason, observed: c.Observed,
		fingerprint: c.Fingerprint, checksHead: c.ChecksHead, conflictHead: c.ConflictHead, lastReviewAt: c.LastReviewAt,
		read: &github.PullRequest{Facts: c.Facts.Facts}, briefMissing: c.BriefMissing, confirmed: c.Confirmed,
		fresh: true, why: c.Why, address: c.Address}, c.Guidance)
}

func (s *sweep) send(ctx context.Context, st step, guidance string) (sent bool, err error) {
	defer func() { s.noteStartProblem(ctx, st, err) }()
	switch st.kind {
	case stepFirstRound:
		return s.firstRound(ctx, st, guidance)
	case stepAddress:
		return s.address(ctx, st, guidance)
	}
	return s.followup(ctx, st, guidance)
}

func withInstructions(reason, sha string) string {
	return reason + " (instructions " + sha + ")"
}

func withGuidance(reason, guidance string) string {
	if guidance == "" {
		return reason
	}
	return reason + "; with your guidance"
}

func (s *sweep) followup(ctx context.Context, st step, guidance string) (bool, error) {
	cfg := s.prConfig(st)
	if st.ws.ID == "" {
		if !cfg.FollowupsWithoutWorkspace {
			s.observe(ctx, st)
			return false, nil
		}
		return s.openRound(ctx, st, guidance, turn.PlannedFollowUp)
	}
	sent, err := s.e.sendTurn(ctx, cfg, turn.Request{Intent: turn.Intent(s.e.roleIntent(builtin.RoleReview)), Trigger: turn.Automated, Planned: turn.PlannedFollowUp, Unit: st.pr.Unit(), Payload: st.payload(), Guidance: guidance,
		BriefMissing: st.briefMissing, Why: st.why})
	if err != nil {
		return false, err
	}
	submitted, verb, err := s.e.startRound(ctx, cfg, st.ws, st.pr, &sent, s.e.sweepOptions(s.e.roleIntent(builtin.RoleReview), roundOptions{confirmed: st.confirmed, trusted: trusts(cfg, st.read), head: trackedHead(st.ws, st.read), pr: st.read}))
	s.e.recordGuidance(ctx, st.pr, guidance)
	if err != nil {
		return false, err
	}
	if st.read != nil {
		s.e.realign(ctx, st.ws, st.read)
	}
	if err := s.e.setReviewStatus(ctx, s.cfg, st.pr, st.ws, true); err != nil {
		s.warnf("%s (%s): setting the board status: %v", st.ws.DisplayName, st.pr, err)
	}
	if !submitted {
		s.logf("%s: %s, but its submission was not observed; the next sweep retries", st.describe(), verb)
		return false, nil
	}
	s.st.RecordFollowup(st.key(), st.mark(), st.fingerprint, s.now)
	s.record(context.WithoutCancel(ctx), st, dinv.KindRoundSent, withInstructions(withGuidance(st.reason, guidance), sent.ID))
	s.logf("%s: %s", st.describe(), verb)
	return true, nil
}

// observe logs a follow-up whose review workspace is gone, once per head,
// and sends nothing.
func (s *sweep) observe(ctx context.Context, st step) {
	head := ""
	if st.read != nil {
		head = st.read.HeadRefOid
	}
	if !s.st.FirstNote("would review "+st.key()+" at "+head, s.now) {
		return
	}
	if s.e.Rec != nil {
		ev := dinv.Event{PR: st.pr, Kind: dinv.KindWouldReview, Key: head, At: s.now, Data: st.reason}
		if err := s.e.Rec.Record(ctx, ev); err != nil {
			s.warnf("%s: recording %s: %v", st.pr, dinv.KindWouldReview, err)
		}
	}
	s.logf("would review %s: %s", st.pr, st.reason)
}

func (s *sweep) firstRound(ctx context.Context, st step, guidance string) (bool, error) {
	return s.openRound(ctx, st, guidance, turn.PlannedFirst)
}

// openRound runs a review round that creates the review workspace.
func (s *sweep) openRound(ctx context.Context, st step, guidance string, planned turn.Planned) (bool, error) {
	cfg := s.prConfig(st)
	pr, err := s.openRead(ctx, st)
	if err != nil {
		return false, err
	}
	sent, err := s.e.sendTurn(ctx, cfg, turn.Request{Intent: turn.Intent(s.e.roleIntent(builtin.RoleReview)), Trigger: turn.Automated, Planned: planned, Unit: st.pr.Unit(), Payload: turnPR(pr), Guidance: guidance,
		BriefMissing: st.briefMissing, Why: st.why})
	if err != nil {
		return false, err
	}
	submitted, err := s.e.openReview(ctx, cfg, st.pr, pr, sent, s.e.sweepOptions(s.e.roleIntent(builtin.RoleReview), roundOptions{pr: pr}))
	s.e.recordGuidance(ctx, st.pr, guidance)
	if err != nil {
		return false, err
	}
	if !submitted {
		s.logf("%s: created, but its submission was not observed; check the session", st.describe())
		return false, nil
	}
	s.st.RecordFollowup(st.key(), st.mark(), st.fingerprint, s.now)
	s.record(context.WithoutCancel(ctx), st, dinv.KindRoundSent, withInstructions(withGuidance(st.reason, guidance), sent.ID))
	s.logf("%s", st.describe())
	return true, nil
}

// openRead is the open pull request a round that creates its workspace is
// about: the facts' read when fresh, else read from GitHub.
func (s *sweep) openRead(ctx context.Context, st step) (*github.PullRequest, error) {
	if st.fresh && st.read != nil {
		return st.read, nil
	}
	return s.e.openPR(ctx, st.pr)
}

// observeAddress records a triage round on a pull request without a
// workspace once per fingerprint of its reviews, and sends nothing.
func (s *sweep) observeAddress(ctx context.Context, st step) {
	if !s.st.FirstNote("would address "+st.key()+" at "+st.fingerprint, s.now) {
		return
	}
	if s.e.Rec != nil {
		ev := dinv.Event{PR: st.pr, Kind: dinv.KindWouldAddress, Key: st.fingerprint, At: s.now, Data: st.reason}
		if err := s.e.Rec.Record(ctx, ev); err != nil {
			s.warnf("%s: recording %s: %v", st.pr, dinv.KindWouldAddress, err)
		}
	}
	s.logf("would address %s: %s", st.pr, st.reason)
}

func (s *sweep) address(ctx context.Context, st step, guidance string) (bool, error) {
	cfg := s.prConfig(st)
	if st.ws.ID == "" {
		if !cfg.AddressWithoutWorkspace {
			s.observeAddress(ctx, st)
			return false, nil
		}
		pr, err := s.openRead(ctx, st)
		if err != nil {
			return false, err
		}
		ws, action, err := s.e.addressWorkspace(ctx, cfg, st.pr, pr, func() (bool, error) { return true, nil })
		if err != nil {
			return false, err
		}
		if action != "" {
			s.logf("%s: %s", st.pr, action)
		}
		st.ws = ws
	}
	r := turn.Request{Intent: turn.Intent(s.e.roleIntent(builtin.RoleAddress)), Trigger: turn.Automated, Unit: st.pr.Unit(), Payload: st.payload(), Guidance: guidance, BriefMissing: st.briefMissing, Why: st.why}
	submitted, verb, turnID, err := s.e.startAddress(ctx, cfg, st.ws, r, s.e.sweepOptions(s.e.roleIntent(builtin.RoleAddress), roundOptions{trusted: trusts(cfg, st.read), pr: st.read}))
	if err != nil {
		return false, err
	}
	if !submitted {
		s.logf("%s: %s, but its submission was not observed; the next sweep retries", st.describe(), verb)
		return false, nil
	}
	s.st.RecordFollowup(st.key(), st.mark(), st.fingerprint, s.now)
	s.record(context.WithoutCancel(ctx), st, dinv.KindRoundSent, withInstructions(withGuidance(st.reason, guidance), turnID))
	s.st.SetHeads(st.key(), st.checksHead, st.conflictHead)
	s.logf("%s: %s", st.describe(), verb)
	return true, nil
}

// prConfig applies the project settings of the step's PR, and the agent home
// of the session that asked for its review when no project sets one.
func (s *sweep) prConfig(st step) config.Config {
	cfg := s.cfg.For(st.pr.Owner, st.pr.Repo)
	if cfg.AgentHome == "" {
		cfg.AgentHome = s.st.AgentHome(st.key())
	}
	return cfg
}
