package views

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/view"
)

// Books is what the views read of the turns.
type Books interface {
	// Books returns the book of each ref, or of every pull request with a
	// turn when none is named.
	Books(ctx context.Context, refs ...unit.Unit) (map[unit.Unit]turn.Book, error)
}

// Intents is the registry the views read the intents of each board from.
type Intents interface {
	All() []dintent.Def
}

type Sink struct {
	ViewSink
	Placements PlacementReader // nil for a push-only sink
}

// ItemError is a sink's failure to write one item. A sink joins one per
// failed item into Apply's error, so only those items are retried; an error
// that names no item fails every item the sink did not apply.
type ItemError struct {
	Key view.Key
	Err error
}

func (e *ItemError) Error() string {
	return fmt.Sprintf("%s %s: %v", e.Key.Unit.Label(), e.Key.Role, e.Err)
}

func (e *ItemError) Unwrap() error { return e.Err }

const placementTimeout = 10 * time.Second

// FlushDebounce is how long a sink's worker waits after a wake before it
// flushes, so the wakes of one round share a flush.
const FlushDebounce = time.Second

// Service projects every pull request's book to each sink through the
// sink's own queue, so a failing sink never delays another or the caller.
type Service struct {
	Books    Books
	Intents  Intents
	State    State
	Sinks    []Sink
	Pub      port.Publisher
	Clock    port.Clock
	Log      func(format string, args ...any)
	Backoff  func(failures int) time.Duration              // nil: 1 s doubling to 5 minutes
	Paused   func(ctx context.Context, ref unit.Unit) bool // true keeps ref's items queued, unapplied
	Facts    Facts                                         // nil projects the turns alone
	Changes  port.Changes                                  // nil, like a set Facts, reconciles every pull request every time
	Debounce time.Duration                                 // 0 flushes at once on a wake; production sets FlushDebounce

	once    sync.Once
	workers []*worker

	reconcileMu sync.Mutex
	reconciled  bool
	lastGen     port.Generation
}

type entry struct {
	item     view.Item
	force    bool
	forceGen uint64 // the gen of the put that set force
	gen      uint64
	failures int
	retryAt  time.Time
}

type worker struct {
	sink Sink
	wake chan struct{}

	mu    sync.Mutex
	queue map[view.Key]entry
	gen   uint64
}

func (s *Service) init() []*worker {
	s.once.Do(func() {
		for _, sk := range s.Sinks {
			s.workers = append(s.workers, &worker{sink: sk, wake: make(chan struct{}, 1), queue: map[view.Key]entry{}})
		}
	})
	return s.workers
}

// Run runs one worker per sink until ctx ends.
func (s *Service) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, w := range s.init() {
		wg.Go(func() { s.work(ctx, w) })
	}
	wg.Wait()
}

// Changed recomputes the items of refs and queues those that differ from
// what each sink last received.
func (s *Service) Changed(ctx context.Context, refs ...unit.Unit) { s.enqueue(ctx, false, refs) }

// RoundStarted queues ref's items for every sink with force, so the column
// is written whatever the card shows and the override is cleared.
func (s *Service) RoundStarted(ctx context.Context, ref unit.Unit) {
	s.enqueue(ctx, true, []unit.Unit{ref})
}

func (s *Service) enqueue(ctx context.Context, force bool, refs []unit.Unit) {
	if len(refs) == 0 {
		return
	}
	books, err := s.Books.Books(ctx, refs...)
	if err != nil {
		s.logf("views: read books: %v", err)
		return
	}
	facts := s.newFacts()
	for _, w := range s.init() {
		applied, err := s.State.Applied(ctx, w.sink.Name())
		if err != nil {
			s.logf("views: %s: read applied: %v", w.sink.Name(), err)
			continue
		}
		placed := s.placed(ctx, w)
		var items []view.Item
		for _, ref := range refs {
			if b, ok := books[ref]; ok {
				items = append(items, s.desired(ctx, b, applied, placed, facts)...)
			}
		}
		queued := 0
		w.mu.Lock()
		for _, it := range items {
			a, have := applied[it.Key()]
			if _, pending := w.queue[it.Key()]; !force && !pending && have && same(it, a) {
				continue
			}
			w.put(it, force)
			queued++
		}
		w.mu.Unlock()
		if queued > 0 {
			w.signal()
		}
	}
}

// Reconcile queues, for the pull requests that may differ, each item that
// differs from what the sink last received or from where the user placed its
// card, and returns how many it queued. With a change log, the pull requests
// are those noted since the last reconcile and those whose card sits where
// the sink did not put it; every pull request with a turn on the first run,
// after a config or all note, with Facts, and without a log. Sinks are read concurrently, so a
// slow one does not hold back another's queue.
func (s *Service) Reconcile(ctx context.Context) (rewritten int, err error) {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	var gen port.Generation
	var since port.Change
	full := s.Changes == nil || s.Facts != nil || !s.reconciled
	if s.Changes != nil {
		gen = s.Changes.Gen()
		since = s.Changes.Since(s.lastGen, s.reconcileTopics())
		full = full || since.All || since.Has(port.TopicConfig)
	}
	var all map[unit.Unit]turn.Book
	if full {
		if all, err = s.Books.Books(ctx); err != nil {
			return 0, err
		}
		if all == nil {
			all = map[unit.Unit]turn.Book{}
		}
	}
	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup
	facts := s.newFacts()
	for _, w := range s.init() {
		wg.Go(func() {
			n, err := s.reconcileSink(ctx, w, all, since, facts)
			w.signal()
			mu.Lock()
			defer mu.Unlock()
			rewritten += n
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", w.sink.Name(), err))
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return rewritten, err
	}
	s.reconciled, s.lastGen = true, gen
	return rewritten, nil
}

func (s *Service) reconcileTopics() port.Topic {
	return port.TopicTurns | port.TopicConfig | port.TopicFollow
}

type sinkRead struct {
	applied      map[view.Key]view.Applied
	appliedErr   error
	placements   map[unit.Unit]string
	placementErr error
}

// reconcileSink reconciles the books, or when nil the pull requests noted in
// since and those whose card on w drifted.
func (s *Service) reconcileSink(ctx context.Context, w *worker, books map[unit.Unit]turn.Book, since port.Change, facts *factsCache) (int, error) {
	var r sinkRead
	r.applied, r.appliedErr = s.State.Applied(ctx, w.sink.Name())
	if r.appliedErr == nil {
		r.placements, r.placementErr = s.readPlacements(ctx, w)
	}
	if books == nil {
		moved := map[unit.Unit]bool{}
		for _, u := range since.Units {
			moved[u] = true
		}
		for _, u := range drifted(w, r) {
			moved[u] = true
		}
		books = map[unit.Unit]turn.Book{}
		if len(moved) > 0 && r.appliedErr == nil {
			var err error
			if books, err = s.Books.Books(ctx, slices.Collect(maps.Keys(moved))...); err != nil {
				return 0, err
			}
		}
	}
	return s.reconcile(ctx, w, r, books, facts)
}

// drifted is the pull requests whose only applied item sits in a column the
// sink did not put it in.
func drifted(w *worker, r sinkRead) []unit.Unit {
	if r.appliedErr != nil || len(r.placements) == 0 {
		return nil
	}
	items := map[unit.Unit][]view.Applied{}
	for _, a := range r.applied {
		items[a.Unit] = append(items[a.Unit], a)
	}
	var out []unit.Unit
	for u, as := range items {
		if len(as) == 1 && w.misplaced(view.Item{Unit: u, Role: as[0].Role}, as[0], r.placements) {
			out = append(out, u)
		}
	}
	return out
}

// reconcile compares without placements when the sink cannot read them, and
// still returns that error.
func (s *Service) reconcile(ctx context.Context, w *worker, r sinkRead, books map[unit.Unit]turn.Book, facts *factsCache) (int, error) {
	if r.appliedErr != nil {
		return 0, r.appliedErr
	}
	applied, placements, placementErr := r.applied, r.placements, r.placementErr
	var placed map[unit.Unit]view.Status
	if s.Facts != nil {
		placed = s.statuses(w, placements)
	}
	wanted := make(map[unit.Unit][]view.Item, len(books))
	for u, b := range books {
		wanted[u] = s.desired(ctx, b, applied, placed, facts)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, items := range wanted {
		for _, it := range items {
			a, have := applied[it.Key()]
			switch {
			case !have || !same(it, a):
				w.put(it, false)
			case len(items) == 1 && w.misplaced(it, a, placements):
				w.requeue(it)
			default:
				continue
			}
			n++
		}
	}
	return n, placementErr
}

// readPlacements is the user's column of each card, nil when the sink cannot
// read them; the error is the read's.
func (s *Service) readPlacements(ctx context.Context, w *worker) (map[unit.Unit]string, error) {
	if w.sink.Placements == nil {
		return nil, nil
	}
	pctx, cancel := context.WithTimeout(ctx, placementTimeout)
	defer cancel()
	placements, err := w.sink.Placements.Placements(pctx)
	if err != nil {
		return nil, err
	}
	return placements, nil
}

// placed is the status of each card on the sink's board; read only when the
// facts decide author columns, and empty when the sink cannot tell.
func (s *Service) placed(ctx context.Context, w *worker) map[unit.Unit]view.Status {
	if s.Facts == nil || w.sink.Placements == nil {
		return nil
	}
	placements, err := s.readPlacements(ctx, w)
	if err != nil {
		s.logf("views: %s: read placements: %v", w.sink.Name(), err)
		return nil
	}
	return s.statuses(w, placements)
}

func (s *Service) statuses(w *worker, placements map[unit.Unit]string) map[unit.Unit]view.Status {
	sr, ok := w.sink.ViewSink.(StatusReader)
	if !ok {
		return nil
	}
	out := make(map[unit.Unit]view.Status, len(placements))
	for u, column := range placements {
		if st := sr.Status(view.AuthorRole, column); st != view.None {
			out[u] = st
		} else {
			out[u] = view.Elsewhere
		}
	}
	return out
}

// misplaced says whether the card's placement disagrees with a, what the
// sink last wrote: outside the managed vocabulary the override must name the
// placement; inside it there must be no override and the card must sit in
// the column written.
func (w *worker) misplaced(it view.Item, a view.Applied, placements map[unit.Unit]string) bool {
	p, placed := placements[it.Unit]
	switch {
	case !placed:
		return false
	case p != "" && !w.sink.Manages(it.Role, p):
		return a.Override != p
	}
	return a.Override != "" || p != a.Column
}

// desired is the book's items that have a status; the author item reads the
// status the sink last applied to it. A finished pull request's review item
// is completed.
func (s *Service) desired(ctx context.Context, b turn.Book, applied map[view.Key]view.Applied, placed map[unit.Unit]view.Status, facts *factsCache) []view.Item {
	last := applied[view.Key{Unit: b.Unit, Role: view.AuthorRole}].Status
	if p, ok := placed[b.Unit]; ok {
		last = p
		if p == view.NeedsMe && applied[view.Key{Unit: b.Unit, Role: view.AuthorRole}].Status != view.NeedsMe {
			last = view.Elsewhere
		}
	}
	review, author := s.board(view.ReviewRole), s.board(view.AuthorRole)
	var f *view.AuthorFacts
	if s.Facts != nil {
		f = s.authorFacts(ctx, b, last, view.HasTurn(b, author), facts)
	}
	ri := view.Review(b, review)
	if f != nil && f.Finished() && (ri.Status != view.None || f.ReviewWorkspace) {
		ri.Status, ri.Unread = view.Completed, false
	}
	var out []view.Item
	for _, it := range []view.Item{ri, view.Author(b, author, last, f)} {
		if it.Status != view.None {
			out = append(out, it)
		}
	}
	return out
}

// authorFacts reads the facts once, and again with the checkout when the
// column asks for it and the unit has an author turn; a failed read projects
// the turns alone.
func (s *Service) authorFacts(ctx context.Context, b turn.Book, last view.Status, authored bool, cache *factsCache) *view.AuthorFacts {
	f, err := cache.get(ctx, s.Facts, b.Unit, false)
	if err != nil || f == nil {
		if err != nil {
			s.logf("views: %s: facts: %v", b.Unit.Label(), err)
		}
		return nil
	}
	if _, _, _, need := view.AuthorColumn(*f, last); need != view.NoLocal && (authored || f.AuthorWorkspace) {
		if l, err := cache.get(ctx, s.Facts, b.Unit, true); err != nil {
			s.logf("views: %s: local facts: %v", b.Unit.Label(), err)
		} else if l != nil {
			f = l
		}
	}
	return f
}

type factsKey struct {
	unit      unit.Unit
	needLocal bool
}

// factsCache shares one read of a unit's facts among the sinks of a call.
type factsCache struct {
	mu sync.Mutex
	m  map[factsKey]*view.AuthorFacts
}

func (s *Service) newFacts() *factsCache { return &factsCache{m: map[factsKey]*view.AuthorFacts{}} }

func (c *factsCache) get(ctx context.Context, src Facts, u unit.Unit, needLocal bool) (*view.AuthorFacts, error) {
	k := factsKey{u, needLocal}
	c.mu.Lock()
	f, ok := c.m[k]
	c.mu.Unlock()
	if ok {
		return f, nil
	}
	f, err := src.Author(ctx, u, needLocal)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.m[k] = f
	c.mu.Unlock()
	return f, nil
}

// board is the intents whose manifest puts their turns on the role's board;
// none when no Intents port is wired.
func (s *Service) board(role view.Role) []turn.Intent {
	var out []turn.Intent
	if s.Intents == nil {
		return nil
	}
	for _, d := range s.Intents.All() {
		if d.Board == string(role) {
			out = append(out, turn.Intent(d.Name))
		}
	}
	return out
}

func same(it view.Item, a view.Applied) bool {
	return it.Status == a.Status && it.Unread == a.Unread && it.Summary == a.Summary
}

// put queues it. Queuing the item already there changes nothing; a new item
// or a forced one starts without the backoff of an earlier failure.
func (w *worker) put(it view.Item, force bool) {
	old, ok := w.queue[it.Key()]
	if ok && !force && old.item == it {
		return
	}
	w.gen++
	e := entry{item: it, force: force || old.force, forceGen: old.forceGen, gen: w.gen}
	if force {
		e.forceGen = w.gen
	}
	w.queue[it.Key()] = e
}

// requeue queues it again for a reason the item does not carry, such as a
// card the user moved, so a flush of the same item in flight does not take it
// out of the queue. It keeps the entry's backoff and force.
func (w *worker) requeue(it view.Item) {
	old, ok := w.queue[it.Key()]
	if !ok || old.item != it {
		w.put(it, false)
		return
	}
	w.gen++
	old.gen = w.gen
	w.queue[it.Key()] = old
}

func (w *worker) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// nextRetry is the earliest time a failed item of a repository that is not
// paused is due again; the reconcile after a resume wakes the worker for the
// others.
func (s *Service) nextRetry(ctx context.Context, w *worker) (time.Time, bool) {
	w.mu.Lock()
	snapshot := maps.Clone(w.queue)
	w.mu.Unlock()
	var next time.Time
	for k, e := range snapshot {
		if e.retryAt.IsZero() || (!next.IsZero() && !e.retryAt.Before(next)) || s.paused(ctx, k.Unit) {
			continue
		}
		next = e.retryAt
	}
	return next, !next.IsZero()
}

func (s *Service) paused(ctx context.Context, ref unit.Unit) bool {
	return s.Paused != nil && s.Paused(ctx, ref)
}

func (s *Service) work(ctx context.Context, w *worker) {
	for {
		var retry <-chan time.Time
		var timer *time.Timer
		if next, ok := s.nextRetry(ctx, w); ok {
			timer = time.NewTimer(time.Until(next))
			retry = timer.C
		}
		select {
		case <-ctx.Done():
		case <-w.wake:
			if !s.settle(ctx, w) {
				return
			}
		case <-retry:
		}
		if timer != nil {
			timer.Stop()
		}
		if ctx.Err() != nil {
			return
		}
		s.flush(ctx, w)
	}
}

// settle waits out the debounce after a wake and reports whether ctx is still
// live. Wakes that arrive meanwhile are absorbed: the flush reads the whole queue.
func (s *Service) settle(ctx context.Context, w *worker) bool {
	if s.Debounce <= 0 {
		return true
	}
	t := time.NewTimer(s.Debounce)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
	}
	select {
	case <-w.wake:
	default:
	}
	return true
}

// flush applies, as one batch, every queued item that is due and whose
// repository is not paused. An item the sink applied leaves the queue, one
// it failed waits its backoff, and one it neither applied nor failed is
// dropped.
func (s *Service) flush(ctx context.Context, w *worker) {
	name := w.sink.Name()
	started := time.Now()
	w.mu.Lock()
	snapshot := maps.Clone(w.queue)
	w.mu.Unlock()

	batch := map[view.Key]entry{}
	force := map[unit.Unit]bool{}
	var items []view.Item
	for k, e := range snapshot {
		if e.retryAt.After(started) || s.paused(ctx, k.Unit) {
			continue
		}
		batch[k] = e
		items = append(items, e.item)
		if e.force {
			force[k.Unit] = true
		}
	}
	if len(items) == 0 {
		return
	}
	slices.SortFunc(items, func(a, b view.Item) int {
		return cmp.Or(cmp.Compare(a.Unit.String(), b.Unit.String()), cmp.Compare(a.Role, b.Role))
	})

	prev, err := s.State.Applied(ctx, name)
	if err != nil {
		s.logf("views: %s: read applied: %v", name, err)
		s.fail(w, batch, func(view.Key) error { return err }, started)
		return
	}
	applied, applyErr := w.sink.Apply(ctx, items, force)
	now := s.now()
	saved := map[view.Key]bool{}
	failed := map[view.Key]error{}
	for _, a := range applied {
		evs := []port.Event{{Unit: a.Unit, Kind: KindViewApplied, Data: string(a.Status), At: now}}
		if a.Override != prev[a.Key()].Override {
			evs = append(evs, port.Event{Unit: a.Unit, Kind: KindViewOverride, Data: a.Override, At: now})
		}
		if a.At.IsZero() {
			a.At = now
		}
		if err := s.State.SaveApplied(ctx, name, a, evs...); err != nil {
			s.logf("views: %s: save %s: %v", name, a.Unit.Label(), err)
			failed[a.Key()] = err
			continue
		}
		saved[a.Key()] = true
		s.publish(evs...)
	}
	named := map[view.Key]error{}
	itemErrors(applyErr, named)
	for k := range batch {
		switch {
		case saved[k]:
		case named[k] != nil:
			failed[k] = named[k]
		case applyErr != nil && len(named) == 0:
			failed[k] = applyErr
		}
	}
	if applyErr != nil {
		s.logf("views: %s: %v", name, applyErr)
	}

	var evs []port.Event
	for k, err := range failed {
		evs = append(evs, port.Event{Unit: k.Unit, Kind: KindViewSinkFailed, Data: name + ": " + err.Error(), At: now})
	}
	if len(evs) > 0 {
		if err := s.State.SaveFailure(ctx, evs...); err != nil {
			s.logf("views: %s: save failure: %v", name, err)
		}
		s.publish(evs...)
	}
	s.fail(w, batch, func(k view.Key) error { return failed[k] }, started)
}

// fail puts back the batch's items failedBy names with their next retry and
// takes the others out of the queue; an item a newer one replaced is left to
// the newer one, without a force only the applied copy carried.
func (s *Service) fail(w *worker, batch map[view.Key]entry, failedBy func(view.Key) error, at time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, e := range batch {
		cur, ok := w.queue[k]
		switch {
		case !ok:
		case cur.gen != e.gen:
			if failedBy(k) == nil && e.force && cur.item == e.item && cur.forceGen == e.forceGen {
				cur.force = false
				w.queue[k] = cur
			}
		case failedBy(k) != nil:
			cur.failures = e.failures + 1
			cur.retryAt = at.Add(s.backoff(cur.failures))
			w.queue[k] = cur
		default:
			delete(w.queue, k)
		}
	}
}

func itemErrors(err error, out map[view.Key]error) {
	var ie *ItemError
	switch u := err.(type) {
	case nil:
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			itemErrors(e, out)
		}
	default:
		if errors.As(err, &ie) {
			out[ie.Key] = ie.Err
		}
	}
}

func (s *Service) backoff(failures int) time.Duration {
	if s.Backoff != nil {
		return s.Backoff(failures)
	}
	const ceiling = 5 * time.Minute
	if failures > 10 {
		return ceiling
	}
	return min(time.Second<<(failures-1), ceiling)
}

func (s *Service) now() time.Time {
	if s.Clock == nil {
		return time.Now()
	}
	return s.Clock.Now()
}

func (s *Service) publish(evs ...port.Event) {
	if s.Pub != nil && len(evs) > 0 {
		s.Pub.Publish(evs...)
	}
}

func (s *Service) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}
