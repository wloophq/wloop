package turns

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	dusage "github.com/wloophq/wloop/internal/domain/usage"
)

type HeadlessStart struct {
	Unit     unit.Unit
	Intent   turn.Intent
	Turn     string
	Trigger  turn.Trigger
	Guidance string
	Workflow string
	// Role is the role the turn's intent holds, "" none.
	Role  string
	Items []string
	Entry string
	// ResumeOf is the headless turn whose question this run answers, 0 none.
	ResumeOf int64
}

type HeadlessEnd struct {
	Rec    record.Record
	Answer Answer // cost, tokens, model
	Err    error
}

// UsageWriter stores a turn's usage rows and marks it read by source.
type UsageWriter interface {
	SaveUsage(ctx context.Context, seq int64, rows []dusage.Model, source string, at time.Time, evs ...port.Event) error
}

const (
	// UsageHeadless is the usage_source of a headless turn's usage, read from its call's answer.
	UsageHeadless = "headless"
	// Interrupted is the summary of a headless turn a restart cut short.
	Interrupted = "interrupted by a restart"
	itemsInput  = "items"
	entryInput  = "entry"
)

// ErrHeadlessBusy is BeginHeadless's refusal while the unit has a headless turn started.
var ErrHeadlessBusy = errors.New("the unit already has a headless turn started")

func headless(t turn.Turn) bool { return t.Mode == turn.ModeHeadless }

// running is a headless turn whose call has not ended.
func running(t turn.Turn) bool {
	return headless(t) && (t.State == turn.Sent || t.State == turn.Started)
}

// refuseOverRunning refuses a send of intent i while a headless turn of i runs on the book's unit.
func refuseOverRunning(b *turn.Book, i turn.Intent) error {
	if slices.ContainsFunc(b.Turns, func(t turn.Turn) bool { return running(t) && t.Intent == i }) {
		return fmt.Errorf("%w: %s runs %s headless; wait for it to end", ErrHeadlessBusy, b.Unit.Label(), i)
	}
	return nil
}

// ItemsOf are the item ids the turn was sent about, kept in its inputs.
func ItemsOf(t turn.Turn) []string {
	var out []string
	switch v := t.Inputs[itemsInput].(type) {
	case []string:
		out = slices.Clone(v)
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// EntryOf is the GTD entry the turn was asked from, kept in its inputs; "" none.
func EntryOf(t turn.Turn) string {
	s, _ := t.Inputs[entryInput].(string)
	return s
}

// bound is inputs with items and entry kept under their reserved names.
func bound(inputs map[string]any, items []string, entry string) map[string]any {
	if len(items) == 0 && entry == "" {
		return inputs
	}
	out := maps.Clone(inputs)
	if out == nil {
		out = map[string]any{}
	}
	if len(items) > 0 {
		out[itemsInput] = slices.Clone(items)
	}
	if entry != "" {
		out[entryInput] = entry
	}
	return out
}

// BeginHeadless stores a turn sent and started with Mode headless and no handle, with Items in the
// turn's Inputs under "items"; ErrHeadlessBusy when the unit already has a headless turn started.
func (s *Service) BeginHeadless(ctx context.Context, h HeadlessStart) (int64, error) {
	if h.Turn == "" {
		return 0, errors.New("a turn needs an id")
	}
	s.mu.Lock()
	b, err := s.book(ctx, h.Unit)
	if err != nil {
		s.mu.Unlock()
		return 0, err
	}
	if slices.ContainsFunc(b.Turns, func(t turn.Turn) bool { return running(t) || (!headless(t) && t.Intent == h.Intent && t.State.Open()) }) {
		s.mu.Unlock()
		return 0, fmt.Errorf("%w: %s", ErrHeadlessBusy, h.Unit.Label())
	}
	now := s.now()
	b.Send(turn.Turn{Intent: h.Intent, ID: h.Turn, Trigger: h.Trigger, Guidance: h.Guidance, ResumeOf: h.ResumeOf,
		Inputs: bound(map[string]any{itemsInput: append([]string{}, h.Items...)}, nil, h.Entry), SentAt: now, Mode: turn.ModeHeadless, Workflow: h.Workflow, Role: h.Role})
	if _, err := b.Start(h.Turn, "", now); err != nil {
		s.mu.Unlock()
		return 0, err
	}
	evs := []port.Event{
		s.event(h.Unit, KindTurnSent, h.Turn, string(h.Intent)),
		s.event(h.Unit, KindTurnStarted, h.Turn, string(turn.ModeHeadless)),
	}
	saved, err := s.Store.Save(ctx, b, evs...)
	if err != nil {
		s.mu.Unlock()
		return 0, err
	}
	t, _ := saved.ByID(h.Turn)
	if s.begun == nil {
		s.begun = map[int64]unit.Unit{}
	}
	s.begun[t.Seq] = h.Unit
	s.mu.Unlock()
	s.publish(evs...)
	s.changed(ctx, h.Unit)
	return t.Seq, nil
}

// EndHeadless stores the record (failed with Err as its summary when set, or with the problems of a record
// that does not fit the intent's rules), applies its ledger ops as a turn_end does, writes turn_usage from
// Answer with usage_source "headless" and emits the usual turn events.
func (s *Service) EndHeadless(ctx context.Context, seq int64, e HeadlessEnd) (Recorded, error) {
	t, u, err := s.headlessTurn(ctx, seq)
	defer s.forget(seq)
	if err != nil {
		if t.Seq != 0 {
			s.saveUsage(seq, t, e.Answer)
		}
		return Recorded{}, err
	}
	rec := e.Rec
	if e.Err != nil {
		rec = failedRecord(e.Err.Error(), "")
	}
	req := RecordRequest{Turn: t.ID, Unit: u, Via: UsageHeadless, Record: rec}
	got, err := s.recordEnd(ctx, req, true)
	var problems record.Problems
	if errors.As(err, &problems) {
		req.Record = failedRecord("the answer does not fit the intent's record: "+problems.Error(), problems.Error())
		got, err = s.recordEnd(ctx, req, true)
	}
	if err != nil {
		return got, err
	}
	s.saveUsage(seq, t, e.Answer)
	return got, nil
}

// saveUsage writes the call's usage on turn seq whatever became of the turn, so the daily cap counts every call.
func (s *Service) saveUsage(seq int64, t turn.Turn, a Answer) {
	if s.Usage == nil {
		return
	}
	if err := s.Usage.SaveUsage(context.Background(), seq, usageRows(t, a, s.now()), UsageHeadless, s.now()); err != nil {
		s.logf("headless turn %d usage: %v", seq, err)
	}
}

func failedRecord(why, resume string) record.Record {
	if resume == "" {
		resume = why
	}
	return record.Record{State: record.Failed, Summary: oneLine(why, record.SummaryMax), Resume: cutRunes(resume, record.ResumeMax)}
}

func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func usageRows(t turn.Turn, a Answer, now time.Time) []dusage.Model {
	if a.Model == "" && a.CostUSD == nil && a.Tokens == nil {
		return nil
	}
	m := dusage.Model{Model: a.Model, Messages: 1, CostUSD: a.CostUSD, First: t.StartedAt, Last: now}
	if a.Tokens != nil {
		m.Tokens = *a.Tokens
	}
	return []dusage.Model{m}
}

// headlessTurn finds turn seq, a started headless one; a headless turn that ended or was replaced or
// dismissed meanwhile comes back with a Refusal.
func (s *Service) headlessTurn(ctx context.Context, seq int64) (turn.Turn, unit.Unit, error) {
	u, ok, err := s.headlessUnit(ctx, seq)
	if err != nil || !ok {
		return turn.Turn{}, unit.Unit{}, cmp.Or(err, fmt.Errorf("%w: no turn %d", turn.ErrNoTurn, seq))
	}
	b, err := s.book(ctx, u)
	if err != nil {
		return turn.Turn{}, unit.Unit{}, err
	}
	t, ok := bySeq(&b, seq)
	switch {
	case !ok || !headless(*t):
		return turn.Turn{}, unit.Unit{}, fmt.Errorf("%w: no headless turn %d", turn.ErrNoTurn, seq)
	case t.State != turn.Started:
		return *t, u, Refusal{fmt.Sprintf("headless turn %d on %s is %s; its answer changes nothing", seq, u.Label(), t.State)}
	}
	return *t, u, nil
}

// headlessUnit is the unit of headless turn seq: the one BeginHeadless stored it on in this process, else
// the open unit whose book holds it.
func (s *Service) headlessUnit(ctx context.Context, seq int64) (unit.Unit, bool, error) {
	s.mu.Lock()
	u, ok := s.begun[seq]
	s.mu.Unlock()
	if ok {
		return u, true, nil
	}
	open, err := s.Store.Open(ctx)
	if err != nil {
		return unit.Unit{}, false, err
	}
	books, err := s.Store.Books(ctx, open...)
	if err != nil {
		return unit.Unit{}, false, err
	}
	for u, b := range books {
		if _, ok := bySeq(&b, seq); ok {
			return u, true, nil
		}
	}
	return unit.Unit{}, false, nil
}

func (s *Service) forget(seq int64) {
	s.mu.Lock()
	delete(s.begun, seq)
	s.mu.Unlock()
}

// RunningHeadless counts the headless turns started and not ended, whichever process started them.
func (s *Service) RunningHeadless(ctx context.Context) (int, error) {
	open, err := s.Store.Open(ctx)
	if err != nil {
		return 0, err
	}
	books, err := s.Store.Books(ctx, open...)
	if err != nil {
		return 0, err
	}
	var n int
	for _, b := range books {
		for _, t := range b.Turns {
			if running(t) {
				n++
			}
		}
	}
	return n, nil
}

// EndInterruptedHeadless ends failed, as interrupted by a restart, every headless turn left started,
// charging each perCallUSD since its cost is unknown; the daemon calls it at start, before any headless run.
func (s *Service) EndInterruptedHeadless(ctx context.Context, perCallUSD float64) (int, error) {
	open, err := s.Store.Open(ctx)
	if err != nil {
		return 0, err
	}
	books, err := s.Store.Books(ctx, open...)
	if err != nil {
		return 0, err
	}
	var n int
	var errs []error
	for _, b := range books {
		for _, t := range b.Turns {
			if !headless(t) || t.State != turn.Started {
				continue
			}
			end := HeadlessEnd{Err: errors.New(Interrupted)}
			if perCallUSD > 0 {
				end.Answer.CostUSD = &perCallUSD
			}
			if _, err := s.EndHeadless(ctx, t.Seq, end); err != nil {
				errs = append(errs, err)
				continue
			}
			n++
		}
	}
	return n, errors.Join(errs...)
}
