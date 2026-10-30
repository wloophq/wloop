package collect

import (
	"context"
	"fmt"
	"time"

	"github.com/wloophq/wloop/plugins/github/api"
)

// stub is an object a pass knows the version of; light is what the
// object's search returned beyond it, nil for a stored row's.
type stub struct {
	object, version string
	light           *api.Stub
}

// run is one two-phase pass: discover from the stored cursor, the stubs of
// the watched objects the discovery did not return, compare each object's
// version with the stored one, read what moved or is unknown, save (the
// light facts of every stub, the read, the cursor), and see every object
// returned.
func (c *Collector) run(ctx context.Context, s *source, st *sourceStore, now time.Time) error {
	c.pass.Lock()
	defer c.pass.Unlock()
	cursor, err := st.Cursor(ctx)
	if err != nil {
		return fmt.Errorf("fast pass: cursor: %w", err)
	}
	discovered, next, err := s.Discover(ctx, cursor)
	if err != nil {
		return fmt.Errorf("fast pass: discover: %w", err)
	}
	stubs := dedupe(discovered)
	watched, err := st.Watched(ctx)
	if err != nil {
		return fmt.Errorf("fast pass: watched: %w", err)
	}
	found := make(map[string]bool, len(stubs))
	for _, x := range stubs {
		found[x.object] = true
	}
	stale := map[string]bool{}
	var check []string
	for _, w := range watched {
		if w.version == "" {
			stale[w.object] = true
		}
		if !found[w.object] {
			found[w.object] = true
			check = append(check, w.object)
		}
	}
	if len(check) > 0 {
		more, err := s.Stubs(ctx, check)
		if err != nil {
			return fmt.Errorf("fast pass: stubs: %w", err)
		}
		stubs = dedupe(append(stubs, more...))
	}
	objects := make([]string, len(stubs))
	for i, x := range stubs {
		objects[i] = x.object
	}
	stored, err := st.Versions(ctx, objects)
	if err != nil {
		return fmt.Errorf("fast pass: versions: %w", err)
	}
	var moved []string
	var light []api.Stub
	for _, x := range stubs {
		if v := stored[x.object]; v == "" || stale[x.object] || v != x.version {
			moved = append(moved, x.object)
		}
		if x.light != nil {
			light = append(light, *x.light)
		}
	}
	return c.finish(ctx, s, st, light, moved, objects, next, now)
}

// touch reads and saves objects as run would, keeping the stored cursor.
func (c *Collector) touch(ctx context.Context, s *source, st *sourceStore, objects []string, now time.Time) error {
	c.pass.Lock()
	defer c.pass.Unlock()
	cursor, err := st.Cursor(ctx)
	if err != nil {
		return fmt.Errorf("fast pass: cursor: %w", err)
	}
	objects = distinct(objects)
	return c.finish(ctx, s, st, nil, objects, objects, cursor, now)
}

func (c *Collector) finish(ctx context.Context, s *source, st *sourceStore, light []api.Stub, moved, objects []string, cursor string, now time.Time) error {
	var read readFacts
	if len(moved) > 0 {
		var err error
		if read, err = s.Read(ctx, moved); err != nil {
			return fmt.Errorf("fast pass: read: %w", err)
		}
	}
	if err := st.Save(ctx, light, read, cursor, now); err != nil {
		return fmt.Errorf("fast pass: save: %w", err)
	}
	if len(objects) > 0 {
		if err := st.See(ctx, objects, now); err != nil {
			return fmt.Errorf("fast pass: see: %w", err)
		}
	}
	return nil
}

// dedupe keeps one stub per object, the last one given, in first-seen order.
func dedupe(stubs []stub) []stub {
	at := make(map[string]int, len(stubs))
	out := make([]stub, 0, len(stubs))
	for _, s := range stubs {
		if i, ok := at[s.object]; ok {
			out[i] = s
			continue
		}
		at[s.object] = len(out)
		out = append(out, s)
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
