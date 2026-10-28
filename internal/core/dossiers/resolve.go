package dossiers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Resolution follows the units' open work into their dossiers: a unit with an active dossier, items,
// and no open item or entry gets its closing call and is resolved; a resolved or archived one with open work
// again is reopened. A call the daily cap refuses waits for a later Retry.
type Resolution struct {
	Calls *Calls
	// Items are the unit's items the closing call reads; nil reads none.
	Items func(ctx context.Context, u unit.Unit) ([]items.Item, error)
	Log   func(format string, args ...any)

	mu       sync.Mutex
	closing  map[unit.Unit]bool // closing calls the cap deferred
	compacts map[unit.Unit]bool // compactions the cap deferred
}

// Units follows each of us whose dossier exists.
func (r *Resolution) Units(ctx context.Context, us ...unit.Unit) error {
	var errs []error
	for _, u := range us {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.follow(ctx, u); err != nil {
			errs = append(errs, fmt.Errorf("dossier of %s: %w", u.Label(), err))
		}
	}
	return errors.Join(errs...)
}

// All follows every unit dossier.
func (r *Resolution) All(ctx context.Context) error {
	all, err := r.svc().Store.List(ctx)
	if err != nil {
		return err
	}
	var us []unit.Unit
	for _, d := range all {
		if !d.Owner.Unit.IsZero() {
			us = append(us, d.Owner.Unit)
		}
	}
	return r.Units(ctx, us...)
}

// Retry runs the closing calls and compactions the cap deferred.
func (r *Resolution) Retry(ctx context.Context) error {
	r.mu.Lock()
	closing, compacts := keys(r.closing), keys(r.compacts)
	r.mu.Unlock()
	errs := []error{r.Units(ctx, closing...)}
	for _, u := range compacts {
		errs = append(errs, r.Compact(ctx, u))
	}
	return errors.Join(errs...)
}

func keys(m map[unit.Unit]bool) []unit.Unit {
	out := make([]unit.Unit, 0, len(m))
	for u := range m {
		out = append(out, u)
	}
	slices.SortFunc(out, func(a, b unit.Unit) int { return strings.Compare(a.String(), b.String()) })
	return out
}

// Compact runs u's compaction when its log passed the threshold; a refusal of the cap defers it.
func (r *Resolution) Compact(ctx context.Context, u unit.Unit) error {
	_, err := r.Calls.Compact(ctx, u)
	r.mark(&r.compacts, u, errors.Is(err, ErrOverCap))
	if errors.Is(err, ErrOverCap) {
		return nil
	}
	return err
}

func (r *Resolution) mark(set *map[unit.Unit]bool, u unit.Unit, on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !on {
		delete(*set, u)
		return
	}
	if *set == nil {
		*set = map[unit.Unit]bool{}
	}
	(*set)[u] = true
}

func (r *Resolution) svc() *Service { return r.Calls.Turns.Dossiers }

func (r *Resolution) follow(ctx context.Context, u unit.Unit) error {
	s := r.svc()
	o := dossier.Owner{Unit: u}
	d, ok, err := s.Get(ctx, o)
	if err != nil || !ok {
		return err
	}
	open, err := s.Store.Open(ctx, o)
	if err != nil {
		return err
	}
	switch {
	case d.Status == dossier.Active && !open:
		its, err := r.items(ctx, u)
		if err != nil || (r.Items != nil && len(its) == 0) {
			return err
		}
		err = r.close(ctx, u, its)
		r.mark(&r.closing, u, errors.Is(err, ErrOverCap))
		if errors.Is(err, ErrOverCap) {
			r.logf("dossier of %s: the closing call waits for a later day: %v", u.Label(), err)
			return nil
		}
		if err != nil {
			r.logf("dossier of %s: the closing call failed, notes.md is kept: %v", u.Label(), err)
		}
		return s.Resolved(ctx, o, s.Clock.Now())
	case (d.Status == dossier.Resolved || d.Status == dossier.Archived) && open:
		r.mark(&r.closing, u, false)
		r.Calls.Turns.locker().Lock()
		defer r.Calls.Turns.locker().Unlock()
		return s.Reopened(ctx, o)
	}
	return nil
}

const closeSchema = `{"type":"object","additionalProperties":false,"required":["outcome","deliverable_url","notes"],"properties":{` +
	`"outcome":{"type":"string","minLength":1,"maxLength":1000,"description":"what the work ended with, in one paragraph"},` +
	`"deliverable_url":{"type":"string","maxLength":2000,"description":"the https URL of the final product in its natural home (the reply, document or pull request), empty when there is none"},` +
	`"notes":{"type":"string","minLength":1,"maxLength":32768,"description":"the whole new notes.md body, Markdown, without frontmatter and without the outcome"}}}`

type closed struct {
	Outcome        *string `json:"outcome"`
	DeliverableURL *string `json:"deliverable_url"`
	Notes          *string `json:"notes"`
}

// closeLogs is how much of the newest log the closing call reads.
const closeLogs = 64 * dossier.KB

// items are u's items; a unit that never had one has no work to close.
func (r *Resolution) items(ctx context.Context, u unit.Unit) ([]items.Item, error) {
	if r.Items == nil {
		return nil, nil
	}
	its, err := r.Items(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("reading the items: %w", err)
	}
	return its, nil
}

func (r *Resolution) close(ctx context.Context, u unit.Unit, its []items.Item) error {
	c := r.Calls
	s, ok, err := c.read(ctx, u, func(_ frontmatter, logs []Entry) ([]Entry, bool) { return lastBytes(logs, closeLogs), true })
	if err != nil || !ok {
		return err
	}
	var a closed
	if err := c.ask(ctx, u, closePrompt(s, u, its), closeSchema, &a); err != nil {
		return err
	}
	if err := a.check(); err != nil {
		return err
	}
	body := "## Outcome\n\n" + strings.TrimSpace(*a.Outcome) + "\n"
	if *a.DeliverableURL != "" {
		body += "\nDeliverable: " + *a.DeliverableURL + "\n"
	}
	return c.rewrite(ctx, s, "close", body+"\n"+strings.TrimSpace(*a.Notes), nil)
}

func (a closed) check() error {
	switch {
	case a.Outcome == nil || strings.TrimSpace(*a.Outcome) == "" || len(*a.Outcome) > 1000:
		return fmt.Errorf("%w: outcome is empty or too long", turns.ErrUnfit)
	case a.Notes == nil || strings.TrimSpace(*a.Notes) == "" || len(*a.Notes) > MaxNotes:
		return fmt.Errorf("%w: notes is empty or over %d bytes", turns.ErrUnfit, MaxNotes)
	case a.DeliverableURL == nil:
		return fmt.Errorf("%w: no deliverable_url", turns.ErrUnfit)
	case *a.DeliverableURL != "":
		p, err := url.Parse(*a.DeliverableURL)
		if err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Host == "" || strings.ContainsAny(*a.DeliverableURL, " \n") {
			return fmt.Errorf("%w: deliverable_url %q is not an http(s) URL", turns.ErrUnfit, *a.DeliverableURL)
		}
	}
	return nil
}

func closePrompt(s snapshot, u unit.Unit, its []items.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The work on %s is over: %s tracks no open item or entry on it any more. You write the closing notes of its dossier, the working memory %s kept for it.\n", u.Label(), brand.Name, brand.Name)
	b.WriteString("Say what the work ended with (outcome), the URL of the final product in its natural home if the items or the files name one (deliverable_url, empty otherwise: never invent one), and rewrite notes.md so a reader who reopens the work later finds the decisions, the links and what was left open.\n")
	var lines []string
	for _, it := range its {
		l := fmt.Sprintf("--- item %s (%s, %s", it.ID, it.Kind, it.State)
		if it.Resolution != "" {
			l += ", " + string(it.Resolution)
		}
		l += ") ---\n" + it.Summary
		if len(it.URLs) > 0 {
			l += "\nurls: " + strings.Join(it.URLs, " ")
		}
		lines = append(lines, l)
	}
	writeData(&b, s, lines...)
	b.WriteString("Answer with one JSON object that fits this schema:\n\n" + closeSchema + "\n")
	return b.String()
}

func (r *Resolution) logf(format string, args ...any) {
	if r.Log != nil {
		r.Log(format, args...)
	}
}

// Watch follows the units of each TopicItems or TopicTasks note, everything once at start, and runs
// Retry every retry, until ctx ends; the subscriber only queues.
func (r *Resolution) Watch(ctx context.Context, changes port.Changes, retry time.Duration) {
	var mu sync.Mutex
	var units []unit.Unit
	all := true
	kick := make(chan struct{}, 1)
	cancel := changes.Subscribe(func(c port.Change) {
		if !c.Has(port.TopicItems | port.TopicTasks) {
			return
		}
		mu.Lock()
		all = all || c.All
		units = append(units, c.Units...)
		mu.Unlock()
		select {
		case kick <- struct{}{}:
		default:
		}
	})
	defer cancel()
	tick := time.NewTicker(retry)
	defer tick.Stop()
	for {
		mu.Lock()
		todo, everything := units, all
		units, all = nil, false
		mu.Unlock()
		var err error
		switch {
		case everything:
			err = r.All(ctx)
		case len(todo) > 0:
			slices.SortFunc(todo, func(a, b unit.Unit) int { return strings.Compare(a.String(), b.String()) })
			err = r.Units(ctx, slices.Compact(todo)...)
		}
		if err != nil && ctx.Err() == nil {
			r.logf("dossiers: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-kick:
		case <-tick.C:
			if err := r.Retry(ctx); err != nil && ctx.Err() == nil {
				r.logf("dossiers: %v", err)
			}
		}
	}
}
