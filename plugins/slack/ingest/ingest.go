// Package ingest drives the Slack source: discovery, change detection by
// object version, detail reads of what moved, and saves through the
// source's store.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/plugins/slack/unit"
)

// Stub is an object as a cheap listing names it.
type Stub struct {
	Object  string // a channel and thread ts for a thread, a conversation for a DM
	Version string // the latest reply of a thread, the latest message of a DM
	Light   Facts  // facts the stub already carries; nil when none
}

// Facts is a source's typed payload; it may name units not seen before.
type Facts interface{ Units() []unit.Unit }

// Source reads the provider's objects. It never writes the store.
type Source interface {
	Provider() unit.ProviderID
	Discover(ctx context.Context, cursor string) (stubs []Stub, next string, err error)
	Stubs(ctx context.Context, objects []string) ([]Stub, error)
	Read(ctx context.Context, objects []string) (Facts, error)
	Viewer(ctx context.Context) (string, error)
}

// SourceStore is the source's own persistence. Only ingest calls it.
type SourceStore interface {
	// Watched are the objects to stub-check every run with their stored versions;
	// an object whose detail is older than its max age has Version "".
	Watched(ctx context.Context) ([]Stub, error)
	Versions(ctx context.Context, objects []string) (map[string]string, error)
	Cursor(ctx context.Context) (string, error)
	// Save stores light facts, then read facts (the read wins for one object),
	// then the cursor. It runs on every pass, so read may be nil and light empty.
	Save(ctx context.Context, light []Facts, read Facts, cursor string, at time.Time) error
	// See stamps the objects as seen at at.
	See(ctx context.Context, objects []string, at time.Time) error
}

// Registration is one source with its store and how its unit keys are
// checked.
type Registration struct {
	Source Source
	Store  SourceStore
	// Valid says why key is not a unit key of the source.
	Valid func(key string) error
}

// Report is what one pass did.
type Report struct {
	Provider          unit.ProviderID
	Discovered, Moved int
	Read              []unit.Unit // units of the read facts, distinct, sorted by Unit.Less
	Cursor            string      // the cursor saved by this pass
}

// Run is one two-phase pass: Discover from the stored cursor, Stubs of Watched
// (minus discovered), compare each object's Version with Versions, Read what
// moved or is unknown, Save (light facts of every stub, read facts, cursor),
// See every object returned. It spends no budget itself: the source's client does.
// Passes of one provider never interleave.
func Run(ctx context.Context, r Registration, now time.Time) (Report, error) {
	rep, err := r.start()
	if err != nil {
		return rep, err
	}
	defer lock(rep.Provider)()
	cursor, err := r.Store.Cursor(ctx)
	if err != nil {
		return rep, r.wrap("cursor", err)
	}
	rep.Cursor = cursor
	discovered, next, err := r.Source.Discover(ctx, cursor)
	if err != nil {
		return rep, r.wrap("discover", err)
	}
	stubs := dedupe(discovered)
	rep.Discovered = len(stubs)
	watched, err := r.Store.Watched(ctx)
	if err != nil {
		return rep, r.wrap("watched", err)
	}
	found := make(map[string]bool, len(stubs))
	for _, s := range stubs {
		found[s.Object] = true
	}
	stale := map[string]bool{}
	var check []string
	for _, w := range watched {
		if w.Version == "" {
			stale[w.Object] = true
		}
		if !found[w.Object] {
			found[w.Object] = true
			check = append(check, w.Object)
		}
	}
	if len(check) > 0 {
		more, err := r.Source.Stubs(ctx, check)
		if err != nil {
			return rep, r.wrap("stubs", err)
		}
		stubs = dedupe(append(stubs, more...))
	}
	objects := objectsOf(stubs)
	stored, err := r.Store.Versions(ctx, objects)
	if err != nil {
		return rep, r.wrap("versions", err)
	}
	var moved []string
	for _, s := range stubs {
		if v := stored[s.Object]; v == "" || stale[s.Object] || v != s.Version {
			moved = append(moved, s.Object)
		}
	}
	var light []Facts
	for _, s := range stubs {
		if s.Light != nil {
			light = append(light, s.Light)
		}
	}
	return r.finish(ctx, rep, light, moved, objects, next, now)
}

// Touch is the push entry: a source that hears of changed objects calls it;
// it reads and saves those objects as Run would, keeping the stored cursor.
// Passes of one provider never interleave.
func Touch(ctx context.Context, r Registration, objects []string, now time.Time) (Report, error) {
	rep, err := r.start()
	if err != nil {
		return rep, err
	}
	defer lock(rep.Provider)()
	cursor, err := r.Store.Cursor(ctx)
	if err != nil {
		return rep, r.wrap("cursor", err)
	}
	rep.Cursor = cursor
	objects = distinct(objects)
	return r.finish(ctx, rep, nil, objects, objects, cursor, now)
}

var passes sync.Map // unit.ProviderID -> *sync.Mutex

// lock serializes the passes of one provider, so a Touch never saves a
// cursor or facts older than a Run that finished during it.
func lock(p unit.ProviderID) (unlock func()) {
	m, _ := passes.LoadOrStore(p, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (r Registration) start() (Report, error) {
	if r.Source == nil || r.Store == nil || r.Valid == nil {
		return Report{}, errors.New("ingest: a registration needs a source, a store and its key check")
	}
	return Report{Provider: r.Source.Provider()}, nil
}

func (r Registration) finish(ctx context.Context, rep Report, light []Facts, moved, objects []string, cursor string, now time.Time) (Report, error) {
	var read Facts
	if len(moved) > 0 {
		var err error
		if read, err = r.Source.Read(ctx, moved); err != nil {
			return rep, r.wrap("read", err)
		}
	}
	units, err := r.units(light, read)
	if err != nil {
		return rep, err
	}
	if err := r.Store.Save(ctx, light, read, cursor, now); err != nil {
		return rep, r.wrap("save", err)
	}
	rep.Moved, rep.Read, rep.Cursor = len(moved), units, cursor
	if len(objects) > 0 {
		if err := r.Store.See(ctx, objects, now); err != nil {
			return rep, r.wrap("see", err)
		}
	}
	return rep, nil
}

// units checks every unit the facts name against the codec and returns the
// read facts' units.
func (r Registration) units(light []Facts, read Facts) ([]unit.Unit, error) {
	for _, f := range light {
		if err := r.valid(f.Units()); err != nil {
			return nil, err
		}
	}
	if read == nil {
		return nil, nil
	}
	us := read.Units()
	if err := r.valid(us); err != nil {
		return nil, err
	}
	out := slices.Clone(us)
	slices.SortFunc(out, compare)
	return slices.Compact(out), nil
}

func (r Registration) valid(us []unit.Unit) error {
	p := r.Source.Provider()
	for _, u := range us {
		if u.Provider != p {
			return fmt.Errorf("ingest: %s: unit %s is not of this provider", p, u)
		}
		if err := r.Valid(u.Key); err != nil {
			return fmt.Errorf("ingest: %s: unit %s: %w", p, u, err)
		}
	}
	return nil
}

func (r Registration) wrap(step string, err error) error {
	return fmt.Errorf("ingest: %s %s: %w", r.Source.Provider(), step, err)
}

func compare(a, b unit.Unit) int { return unit.Compare(a, b) }

// dedupe keeps one stub per object, the last one given, in first-seen order.
func dedupe(stubs []Stub) []Stub {
	at := make(map[string]int, len(stubs))
	out := make([]Stub, 0, len(stubs))
	for _, s := range stubs {
		if i, ok := at[s.Object]; ok {
			out[i] = s
			continue
		}
		at[s.Object] = len(out)
		out = append(out, s)
	}
	return out
}

func objectsOf(stubs []Stub) []string {
	out := make([]string, len(stubs))
	for i, s := range stubs {
		out[i] = s.Object
	}
	return out
}

func distinct(objects []string) []string {
	seen := make(map[string]bool, len(objects))
	var out []string
	for _, o := range objects {
		if !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	return out
}
