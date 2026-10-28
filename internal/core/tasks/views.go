package tasks

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type viewSource struct {
	prefix string
	src    ViewSource
}

// RegisterView serves v from src; ids starting with prefix are read from it.
func (s *Service) RegisterView(v task.View, prefix string, src ViewSource) {
	s.reg.Lock()
	defer s.reg.Unlock()
	s.views[v] = viewSource{prefix: prefix, src: src}
}

func (s *Service) view(v task.View) (ViewSource, bool) {
	s.reg.RLock()
	defer s.reg.RUnlock()
	vs, ok := s.views[v]
	return vs.src, ok
}

func (s *Service) sourceOf(id task.ID) (ViewSource, bool) {
	s.reg.RLock()
	defer s.reg.RUnlock()
	for _, vs := range s.views {
		if vs.prefix != "" && strings.HasPrefix(string(id), vs.prefix) {
			return vs.src, true
		}
	}
	return nil, false
}

// Entries is q's view, from its registered ViewSource or the store; the outbox is empty while none serves it.
// A disabled plugin's entries are left out, as hidden says.
func (s *Service) Entries(ctx context.Context, q task.Query) ([]task.Entry, error) {
	if src, ok := s.view(q.View); ok {
		return src.Entries(ctx, q)
	}
	switch q.View {
	case task.ViewOutbox:
		return nil, nil
	case task.ViewReview, task.ViewOpen:
		return nil, fmt.Errorf("%w: view %q is not served", ErrArgs, q.View)
	}
	es, err := s.d.Store.Entries(ctx, q)
	return s.shown(q.View, es), err
}

// hiddenIn are the views that leave out a disabled plugin's entries; Waiting and the Logbook keep them, read-only.
var hiddenIn = []task.View{task.ViewFocus, task.ViewInbox, task.ViewNext, task.ViewProjects, task.ViewSomeday}

// hider says whether an entry is hidden: it has units, all of a disabled plugin; nil when no plugin is disabled.
func (s *Service) hider() func(task.Entry) bool {
	if s.d.Disabled == nil {
		return nil
	}
	off := s.d.Disabled()
	if len(off) == 0 {
		return nil
	}
	return func(e task.Entry) bool {
		return len(e.Units) > 0 && !slices.ContainsFunc(e.Units, func(u unit.Unit) bool { return !slices.Contains(off, u.Provider) })
	}
}

func (s *Service) shown(v task.View, es []task.Entry) []task.Entry {
	hidden := s.hider()
	if hidden == nil || !slices.Contains(hiddenIn, v) {
		return es
	}
	return slices.DeleteFunc(es, hidden)
}

// hideCounts recounts c's hidden views without a disabled plugin's entries.
func (s *Service) hideCounts(ctx context.Context, c task.Counters, area string) (task.Counters, error) {
	hidden := s.hider()
	if hidden == nil {
		return c, nil
	}
	open, err := s.d.Store.Entries(ctx, task.Query{View: task.ViewOpen})
	if err != nil {
		return task.Counters{}, err
	}
	shown := task.CountsOf(slices.DeleteFunc(open, hidden), area)
	for _, v := range hiddenIn {
		c.Views[v] = shown.Views[v]
	}
	c.Areas, c.Contexts = shown.Areas, shown.Contexts
	return c, nil
}

// Entry reads id from the ViewSource its prefix names, or the store.
func (s *Service) Entry(ctx context.Context, id task.ID) (task.Entry, error) {
	if src, ok := s.sourceOf(id); ok {
		e, found, err := src.Entry(ctx, id)
		if err != nil {
			return task.Entry{}, err
		}
		if !found {
			return task.Entry{}, fmt.Errorf("%w: %s", ErrNoEntry, id)
		}
		return e, nil
	}
	return s.d.Store.Entry(ctx, id)
}

// Counts is Store.Counts plus each ViewSource.Count but review's; Badge is Inbox plus Outbox.
func (s *Service) Counts(ctx context.Context, area string) (task.Counters, error) {
	c, err := s.d.Store.Counts(ctx, area)
	if err != nil {
		return task.Counters{}, err
	}
	return s.withSources(ctx, c, area)
}

// CountsWhere is Counts over the entries keep holds: each view, area and context counts only the entries of its
// list that keep takes, so a client's filter narrows the counters as it narrows the lists.
func (s *Service) CountsWhere(ctx context.Context, area string, keep func(task.Entry) (bool, error)) (task.Counters, error) {
	open, err := s.d.Store.Entries(ctx, task.Query{View: task.ViewOpen})
	if err != nil {
		return task.Counters{}, err
	}
	logbook, err := s.d.Store.Entries(ctx, task.Query{View: task.ViewLogbook})
	if err != nil {
		return task.Counters{}, err
	}
	all := open
	for _, e := range logbook {
		if !slices.ContainsFunc(open, func(o task.Entry) bool { return o.ID == e.ID }) {
			all = append(all, e)
		}
	}
	kept := map[task.ID]bool{}
	count := func(es []task.Entry) ([]task.Entry, error) {
		var out []task.Entry
		for _, e := range es {
			k, seen := kept[e.ID]
			if !seen {
				if k, err = keep(e); err != nil {
					return nil, err
				}
				kept[e.ID] = k
			}
			if k {
				out = append(out, e)
			}
		}
		return out, nil
	}
	c := task.Counters{Area: area, Views: map[task.View]int{task.ViewOutbox: 0}, Areas: map[string]int{}, Contexts: map[string]int{}}
	for _, v := range task.StoredViews {
		es, err := count(s.shown(v, task.Select(all, task.Query{View: v, Area: area})))
		if err != nil {
			return task.Counters{}, err
		}
		c.Views[v] = len(es)
	}
	next, err := count(s.shown(task.ViewNext, task.Select(all, task.Query{View: task.ViewNext})))
	if err != nil {
		return task.Counters{}, err
	}
	for _, e := range next {
		if e.Area != "" {
			c.Areas[e.Area]++
		}
		if area == "" || e.Area == area {
			for _, cx := range e.Contexts {
				c.Contexts[cx]++
			}
		}
	}
	s.reg.RLock()
	views := make(map[task.View]ViewSource, len(s.views))
	for v, vs := range s.views {
		views[v] = vs.src
	}
	s.reg.RUnlock()
	for v, src := range views {
		if v == task.ViewReview {
			continue
		}
		es, err := src.Entries(ctx, task.Query{View: v, Area: area})
		if err != nil {
			return task.Counters{}, err
		}
		if es, err = count(es); err != nil {
			return task.Counters{}, err
		}
		c.Views[v] = len(es)
	}
	c.Badge = c.Views[task.ViewInbox] + c.Views[task.ViewOutbox]
	return c, nil
}

// withSources is c, the stored views' counters without a disabled plugin's entries, with each ViewSource's count
// but review's.
func (s *Service) withSources(ctx context.Context, c task.Counters, area string) (task.Counters, error) {
	if c.Views == nil {
		c.Views = map[task.View]int{}
	}
	c, err := s.hideCounts(ctx, c, area)
	if err != nil {
		return task.Counters{}, err
	}
	c.Views[task.ViewOutbox] = 0
	s.reg.RLock()
	views := make(map[task.View]ViewSource, len(s.views))
	for v, vs := range s.views {
		views[v] = vs.src
	}
	s.reg.RUnlock()
	for v, src := range views {
		if v == task.ViewReview {
			continue
		}
		n, err := src.Count(ctx, area)
		if err != nil {
			return task.Counters{}, err
		}
		c.Views[v] = n
	}
	c.Badge = c.Views[task.ViewInbox] + c.Views[task.ViewOutbox]
	return c, nil
}

// EntriesSince is q's page after since with the counters of q's area: a registered ViewSource's view is always
// Full, at the store's position.
func (s *Service) EntriesSince(ctx context.Context, q task.Query, since Since) (Page, error) {
	src, ok := s.view(q.View)
	switch {
	case ok || q.View == task.ViewOutbox:
		p, err := s.d.Store.EntriesSince(ctx, task.Query{View: task.ViewFocus, Area: q.Area}, Since{})
		if err != nil {
			return Page{}, err
		}
		p.Full, p.Entries, p.Removed = true, nil, nil
		if ok {
			if p.Entries, err = src.Entries(ctx, q); err != nil {
				return Page{}, err
			}
		}
		p.Counters, err = s.withSources(ctx, p.Counters, q.Area)
		return p, err
	case !slices.Contains(task.StoredViews, q.View):
		return Page{}, fmt.Errorf("%w: view %q is not served", ErrArgs, q.View)
	}
	p, err := s.d.Store.EntriesSince(ctx, q, since)
	if err != nil {
		return Page{}, err
	}
	p.Entries = s.shown(q.View, p.Entries)
	p.Counters, err = s.withSources(ctx, p.Counters, q.Area)
	return p, err
}

// Delta is the incremental page of q over all, the entries a store holds for q and every entry changed after
// the client's position (changed says which): the changed entries in q's view, and the changed ids out of it.
// A change to a project, or to one of its actions, counts as a change to all its actions, since the sequential
// rule may move another one into Next. Removed may name ids the client never held.
func Delta(all []task.Entry, q task.Query, changed func(task.ID) bool) (in []task.Entry, removed []task.ID) {
	touched := map[task.ID]bool{}
	for _, e := range all {
		if changed(e.ID) {
			touched[e.ID] = true
			if e.Project != "" {
				touched[e.Project] = true
			}
		}
	}
	hit := func(e task.Entry) bool { return touched[e.ID] || (e.Project != "" && touched[e.Project]) }
	inView := map[task.ID]bool{}
	for _, e := range task.Select(all, q) {
		inView[e.ID] = true
		if hit(e) {
			in = append(in, e)
		}
	}
	seen := map[task.ID]bool{}
	for _, e := range all {
		if hit(e) && !inView[e.ID] && !seen[e.ID] {
			seen[e.ID] = true
			removed = append(removed, e.ID)
		}
	}
	return in, removed
}
