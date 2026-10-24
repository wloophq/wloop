// Package ingesttest is a fake "thread" provider for driving ingest: a
// conversation service, a budgeted client, its Source and its SourceStore.
package ingesttest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
)

// Provider is the fake provider's id.
const Provider unit.ProviderID = "thread"

// The meter resources of the fake client's calls and of the fake store's writes.
const (
	ClientRes meter.Resource = "thread"
	StoreRes  meter.Resource = "thread store"
)

// ErrBudget is the client's refusal once its points are spent.
var ErrBudget = errors.New("thread: budget spent")

// Codec is the unit codec of the fake provider.
func Codec() unit.Codec { return unit.OpaqueCodec{ID: Provider} }

// Thread is one conversation object. A DM thread holds topic segments, each
// its own unit; any other thread is one unit.
type Thread struct {
	Object      string // "<channel> <ts>"
	LatestReply string // fixed-width ts, so it orders as a string
	Replies     []string
	Segments    []string
	Links       []link.Link
}

// Service is the remote conversation service the client talks to.
type Service struct {
	mu      sync.Mutex
	threads map[string]*Thread
	viewer  string
}

func NewService(viewer string) *Service {
	return &Service{threads: map[string]*Thread{}, viewer: viewer}
}

// Post adds a reply at ts to the object's thread, creating it, and sets its
// segments when any are given.
func (s *Service) Post(object, ts, text string, segments ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.threads[object]
	if !ok {
		t = &Thread{Object: object}
		s.threads[object] = t
	}
	t.LatestReply = ts
	t.Replies = append(t.Replies, text)
	if len(segments) > 0 {
		t.Segments = slices.Clone(segments)
	}
}

// SetLinks replaces the links the object's facts name.
func (s *Service) SetLinks(object string, links ...link.Link) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.threads[object]; ok {
		t.Links = slices.Clone(links)
	}
}

func (s *Service) thread(object string) (Thread, bool) {
	t, ok := s.threads[object]
	if !ok {
		return Thread{}, false
	}
	c := *t
	c.Replies, c.Segments, c.Links = slices.Clone(t.Replies), slices.Clone(t.Segments), slices.Clone(t.Links)
	return c, true
}

// Client calls the service, spending its own budget: one point per call
// plus one per object read.
type Client struct {
	S *Service

	mu     sync.Mutex
	budget int64
}

func NewClient(s *Service, budget int64) *Client { return &Client{S: s, budget: budget} }

func (c *Client) spend(ctx context.Context, op string, points int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.budget < points {
		meter.Add(ctx, meter.Sample{Res: ClientRes, Op: op, Err: true})
		return ErrBudget
	}
	c.budget -= points
	meter.Add(ctx, meter.Sample{Res: ClientRes, Op: op, Points: points})
	return nil
}

// Facts is the fake provider's typed payload.
type Facts struct{ Threads []ThreadFacts }

// ThreadFacts is one object's facts. A stub's light facts carry no replies,
// and no units for a thread with segments: segments are found in Read.
type ThreadFacts struct {
	Object, Version string
	Units           []unit.Unit
	Count           int
	Replies         []string
	Links           []link.Link
}

func (f Facts) Units() []unit.Unit {
	var out []unit.Unit
	for _, t := range f.Threads {
		out = append(out, t.Units...)
	}
	return out
}

var _ port.Linker = Facts{}

func (f Facts) Links() []link.Link {
	var out []link.Link
	for _, t := range f.Threads {
		out = append(out, t.Links...)
	}
	return out
}

// UnitsOf are the units a thread holds: one per segment, or the object itself.
func UnitsOf(t Thread) []unit.Unit {
	if len(t.Segments) == 0 {
		return []unit.Unit{{Provider: Provider, Key: t.Object}}
	}
	out := make([]unit.Unit, len(t.Segments))
	for i, seg := range t.Segments {
		out[i] = unit.Unit{Provider: Provider, Key: t.Object + "/" + seg}
	}
	return out
}

// Source is the fake provider's port.Source. It holds no store.
type Source struct{ C *Client }

func (Source) Provider() unit.ProviderID { return Provider }

// Discover returns a stub of every thread whose latest reply is after the cursor.
func (s Source) Discover(ctx context.Context, cursor string) ([]port.Stub, string, error) {
	if err := s.C.spend(ctx, "search", 1); err != nil {
		return nil, "", err
	}
	s.C.S.mu.Lock()
	defer s.C.S.mu.Unlock()
	next := cursor
	var out []port.Stub
	for _, t := range s.C.S.threads {
		if t.LatestReply <= cursor {
			continue
		}
		out = append(out, stub(*t))
		next = max(next, t.LatestReply)
	}
	slices.SortFunc(out, func(a, b port.Stub) int { return compareStrings(a.Object, b.Object) })
	return out, next, nil
}

func (s Source) Stubs(ctx context.Context, objects []string) ([]port.Stub, error) {
	if err := s.C.spend(ctx, "stubs", 1); err != nil {
		return nil, err
	}
	s.C.S.mu.Lock()
	defer s.C.S.mu.Unlock()
	var out []port.Stub
	for _, o := range objects {
		if t, ok := s.C.S.thread(o); ok {
			out = append(out, stub(t))
		}
	}
	return out, nil
}

func (s Source) Read(ctx context.Context, objects []string) (port.Facts, error) {
	if err := s.C.spend(ctx, "read", int64(len(objects))); err != nil {
		return nil, err
	}
	s.C.S.mu.Lock()
	defer s.C.S.mu.Unlock()
	var f Facts
	for _, o := range objects {
		t, ok := s.C.S.thread(o)
		if !ok {
			return nil, fmt.Errorf("thread: %s not found", o)
		}
		f.Threads = append(f.Threads, ThreadFacts{Object: t.Object, Version: t.LatestReply, Units: UnitsOf(t), Count: len(t.Replies), Replies: t.Replies, Links: t.Links})
	}
	return f, nil
}

func (s Source) Viewer(ctx context.Context) (string, error) {
	if err := s.C.spend(ctx, "viewer", 1); err != nil {
		return "", err
	}
	s.C.S.mu.Lock()
	defer s.C.S.mu.Unlock()
	return s.C.S.viewer, nil
}

func stub(t Thread) port.Stub {
	var us []unit.Unit
	if len(t.Segments) == 0 {
		us = UnitsOf(t)
	}
	light := Facts{Threads: []ThreadFacts{{Object: t.Object, Units: us, Count: len(t.Replies)}}}
	return port.Stub{Object: t.Object, Version: t.LatestReply, Light: light}
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Store is the fake provider's port.SourceStore over memory. Every write is
// metered under StoreRes; Save notes the log with the units it changed.
type Store struct {
	Log *change.Log

	mu       sync.Mutex
	threads  map[string]ThreadFacts
	cursor   string
	seen     map[string]time.Time
	stale    map[string]bool
	FailSave error
}

func NewStore(log *change.Log) *Store {
	return &Store{Log: log, threads: map[string]ThreadFacts{}, seen: map[string]time.Time{}, stale: map[string]bool{}}
}

// Expire makes the object's detail older than its max age.
func (s *Store) Expire(object string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stale[object] = true
}

func (s *Store) Watched(ctx context.Context) ([]port.Stub, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]port.Stub, 0, len(s.threads))
	for o, t := range s.threads {
		v := t.Version
		if s.stale[o] {
			v = ""
		}
		out = append(out, port.Stub{Object: o, Version: v})
	}
	slices.SortFunc(out, func(a, b port.Stub) int { return compareStrings(a.Object, b.Object) })
	return out, nil
}

func (s *Store) Versions(ctx context.Context, objects []string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for _, o := range objects {
		if t, ok := s.threads[o]; ok && t.Version != "" {
			out[o] = t.Version
		}
	}
	return out, nil
}

func (s *Store) Cursor(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursor, nil
}

func (s *Store) Save(ctx context.Context, light []port.Facts, read port.Facts, cursor string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.FailSave != nil {
		meter.Add(ctx, meter.Sample{Res: StoreRes, Op: "save", Err: true})
		return s.FailSave
	}
	var changed []unit.Unit
	rows := int64(0)
	for _, f := range light {
		for _, t := range f.(Facts).Threads {
			cur, ok := s.threads[t.Object]
			if ok && cur.Count == t.Count {
				continue
			}
			cur.Object, cur.Count = t.Object, t.Count
			if len(cur.Units) == 0 {
				cur.Units = t.Units
			}
			s.threads[t.Object] = cur
			changed = append(changed, cur.Units...)
			rows++
		}
	}
	if read != nil {
		for _, t := range read.(Facts).Threads {
			delete(s.stale, t.Object)
			if cur, ok := s.threads[t.Object]; ok && cur.Version == t.Version && slices.Equal(cur.Replies, t.Replies) && slices.Equal(cur.Units, t.Units) {
				continue
			}
			s.threads[t.Object] = t
			changed = append(changed, t.Units...)
			rows++
		}
	}
	if cursor != s.cursor {
		s.cursor = cursor
		rows++
	}
	if rows == 0 {
		return nil
	}
	meter.Add(ctx, meter.Sample{Res: StoreRes, Op: "save", Rows: rows})
	if len(changed) > 0 {
		s.Log.Note(port.TopicSourceEvents, changed...)
	}
	return nil
}

func (s *Store) See(ctx context.Context, objects []string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range objects {
		s.seen[o] = at
	}
	meter.Add(ctx, meter.Sample{Res: StoreRes, Op: "see", Rows: int64(len(objects))})
	return nil
}

// Stored is the stored facts of the object.
func (s *Store) Stored(object string) (ThreadFacts, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.threads[object]
	return t, ok
}

// Seen is when the object was last seen.
func (s *Store) Seen(object string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[object]
}

// Links is a port.LinkStore over memory; its writes note the log.
type Links struct {
	Log *change.Log

	mu    sync.Mutex
	links []link.Link
}

func (l *Links) ReplaceFacts(ctx context.Context, from []unit.Unit, links []link.Link, at time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var changed []unit.Unit
	kept := slices.DeleteFunc(slices.Clone(l.links), func(h link.Link) bool {
		gone := h.Origin == link.Facts && slices.Contains(from, h.From)
		if gone {
			changed = append(changed, h.From, h.To)
		}
		return gone
	})
	for _, n := range links {
		n.Origin = link.Facts
		if n.At.IsZero() {
			n.At = at
		}
		kept = append(kept, n)
		changed = append(changed, n.From, n.To)
	}
	l.links = kept
	if len(changed) > 0 && l.Log != nil {
		l.Log.Note(port.TopicLinks, changed...)
	}
	return nil
}

func (l *Links) Put(ctx context.Context, n link.Link) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := slices.IndexFunc(l.links, func(h link.Link) bool {
		return h.From == n.From && h.To == n.To && h.Kind == n.Kind && h.Item == n.Item
	})
	if i >= 0 {
		l.links[i].Evidence, l.links[i].Origin = n.Evidence, n.Origin
	} else {
		l.links = append(l.links, n)
	}
	if l.Log != nil {
		l.Log.Note(port.TopicLinks, n.From, n.To)
	}
	return nil
}

func (l *Links) From(ctx context.Context, u unit.Unit) ([]link.Link, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []link.Link
	for _, h := range l.links {
		if h.From == u {
			out = append(out, h)
		}
	}
	return out, nil
}

func (l *Links) To(ctx context.Context, units []unit.Unit) ([]link.Link, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []link.Link
	for _, h := range l.links {
		if slices.Contains(units, h.To) {
			out = append(out, h)
		}
	}
	slices.SortStableFunc(out, func(a, b link.Link) int {
		if a.From != b.From {
			if a.From.Less(b.From) {
				return -1
			}
			return 1
		}
		return a.At.Compare(b.At)
	})
	return out, nil
}
