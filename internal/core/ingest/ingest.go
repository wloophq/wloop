// Package ingest drives a source: discovery, change detection by object
// version, detail reads of what moved, and saves through the source's store.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Registration is one source with its store and its unit codec.
type Registration struct {
	Source port.Source
	Store  port.SourceStore
	Codec  unit.Codec
	// Units holds every registered codec, disabled plugins' included; a link's To is checked against it.
	Units *unit.Registry
	// Links stores the links the read facts name; nil drops them.
	Links port.LinkStore
}

// Report is what one pass did.
type Report struct {
	Provider          unit.ProviderID
	Discovered, Moved int
	Read              []unit.Unit // units of the read facts, distinct, sorted by Unit.Less
	Cursor            string      // the cursor saved by this pass
	// Links are the links saved by this pass; LinksDropped those whose To did not parse.
	Links, LinksDropped int
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
	var light []port.Facts
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
	if r.Source == nil || r.Store == nil || r.Codec == nil {
		return Report{}, errors.New("ingest: a registration needs a source, a store and a codec")
	}
	rep := Report{Provider: r.Source.Provider()}
	if rep.Provider != r.Codec.Provider() {
		return rep, fmt.Errorf("ingest: source %s registered with the codec of %s", rep.Provider, r.Codec.Provider())
	}
	return rep, nil
}

func (r Registration) finish(ctx context.Context, rep Report, light []port.Facts, moved, objects []string, cursor string, now time.Time) (Report, error) {
	var read port.Facts
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
	links, dropped, err := r.links(read, units)
	if err != nil {
		return rep, err
	}
	if err := r.Store.Save(ctx, light, read, cursor, now); err != nil {
		return rep, r.wrap("save", err)
	}
	rep.Moved, rep.Read, rep.Cursor = len(moved), units, cursor
	if links != nil {
		if err := r.Links.ReplaceFacts(ctx, units, links, now); err != nil {
			return rep, r.wrap("links", err)
		}
		rep.Links, rep.LinksDropped = len(links), dropped
	}
	if len(objects) > 0 {
		if err := r.Store.See(ctx, objects, now); err != nil {
			return rep, r.wrap("see", err)
		}
	}
	return rep, nil
}

// units checks every unit the facts name against the codec and returns the
// read facts' units.
func (r Registration) units(light []port.Facts, read port.Facts) ([]unit.Unit, error) {
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

// links are the read facts' links whose To parses in Units, never nil when the
// facts name links and a link store saves them; dropped counts the others. A
// link from a unit the read facts do not hold fails the pass.
func (r Registration) links(read port.Facts, units []unit.Unit) (_ []link.Link, dropped int, _ error) {
	lk, ok := read.(port.Linker)
	if !ok || r.Links == nil {
		return nil, 0, nil
	}
	out := []link.Link{}
	for _, l := range lk.Links() {
		if _, found := slices.BinarySearchFunc(units, l.From, compare); !found {
			return nil, 0, fmt.Errorf("ingest: %s: link from %s, not a unit of the read facts", r.Codec.Provider(), l.From)
		}
		if !l.Kind.Valid() {
			return nil, 0, fmt.Errorf("ingest: %s: link from %s: unknown kind %q", r.Codec.Provider(), l.From, l.Kind)
		}
		if r.Units == nil {
			dropped++
			continue
		}
		if _, err := r.Units.Parse(string(l.To.Provider), l.To.Key); err != nil {
			dropped++
			continue
		}
		out = append(out, l)
	}
	return out, dropped, nil
}

func (r Registration) valid(us []unit.Unit) error {
	p := r.Codec.Provider()
	for _, u := range us {
		if u.Provider != p {
			return fmt.Errorf("ingest: %s: unit %s is not of this provider", p, u)
		}
		if err := r.Codec.Valid(u.Key); err != nil {
			return fmt.Errorf("ingest: %s: unit %s: %w", p, u, err)
		}
	}
	return nil
}

func (r Registration) wrap(step string, err error) error {
	return fmt.Errorf("ingest: %s %s: %w", r.Source.Provider(), step, err)
}

func compare(a, b unit.Unit) int {
	switch {
	case a.Less(b):
		return -1
	case b.Less(a):
		return 1
	}
	return 0
}

// dedupe keeps one stub per object, the last one given, in first-seen order.
func dedupe(stubs []port.Stub) []port.Stub {
	at := make(map[string]int, len(stubs))
	out := make([]port.Stub, 0, len(stubs))
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

func objectsOf(stubs []port.Stub) []string {
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
