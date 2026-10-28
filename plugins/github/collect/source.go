package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/scope"
	"github.com/wloophq/wloop/plugins/github/store"
)

// source is the GitHub source of one fast run: it reads through the
// collector's client within the run's resolved scope and writes nothing.
type source struct {
	c        *Collector
	resolved scope.Resolved
	now      time.Time
	rep      *Report
	unread   []api.Ref // refs the last Read asked for and GitHub did not return
}

// Discover runs the scope's searches for what moved since the cursor, minus
// sinceMargin, or lists the open pull requests when there is no cursor. A
// pull request no longer open is left to the watched stubs: the stored ones
// are watched, and closed history is the slow sweep's.
func (s *source) Discover(ctx context.Context, cursor string) ([]stub, string, error) {
	var since time.Time
	if cursor != "" {
		at, err := time.Parse(time.RFC3339, cursor)
		if err != nil {
			return nil, "", fmt.Errorf("cursor %q: %w", cursor, err)
		}
		since = at.Add(-sinceMargin)
	}
	qs := s.resolved.Queries(since)
	stubs, failed, err := s.c.search(ctx, qs, api.DiscoveryFirst, s.rep)
	if err != nil {
		return nil, "", err
	}
	if failed > 0 && failed == len(qs) {
		return nil, "", fmt.Errorf("every search failed: %s", strings.Join(s.rep.Warnings, "; "))
	}
	var out []stub
	for _, st := range stubs {
		if st.State == "" || st.State == api.StateOpen {
			out = append(out, portStub(st))
		}
	}
	return out, s.now.UTC().Format(time.RFC3339), nil
}

// Stubs reads the stubs of objects by reference. One GitHub does not return
// is a warning and has no stub; when GitHub refuses it for good (not found,
// forbidden, SAML) it is not asked for again until the next slow sweep.
func (s *source) Stubs(ctx context.Context, objects []string) ([]stub, error) {
	refs, err := refsOf(objects)
	if err != nil {
		return nil, err
	}
	refs = slices.DeleteFunc(refs, s.c.unreadable)
	if len(refs) == 0 {
		return nil, nil
	}
	got, errs, err := s.c.GH.StubsByRef(ctx, refs)
	if err != nil {
		return nil, fmt.Errorf("pull request stubs: %w", err)
	}
	var out []stub
	for _, ref := range refs {
		if err := errs[ref]; err != nil {
			s.rep.Failed++
			s.c.warn(s.rep, "stub %s: %v", ref, err)
			if refused(err) {
				s.c.noteUnreadable([]api.Ref{ref})
			}
			continue
		}
		if st, ok := got[ref]; ok {
			s.rep.Stubs++
			out = append(out, portStub(st))
		}
	}
	return out, nil
}

// Read reads objects in full; one GitHub does not return is a warning and
// lands in unread.
func (s *source) Read(ctx context.Context, objects []string) (readFacts, error) {
	refs, err := refsOf(objects)
	if err != nil {
		return nil, err
	}
	snaps, errs, _, err := s.c.GH.InventoryByRef(ctx, refs)
	if err != nil {
		return nil, fmt.Errorf("read pull requests: %w", err)
	}
	var facts readFacts
	for _, ref := range refs {
		if err := errs[ref]; err != nil {
			s.rep.Failed++
			s.c.warn(s.rep, "read %s: %v", ref, err)
			s.unread = append(s.unread, ref)
			continue
		}
		snap, ok := snaps[ref]
		if !ok {
			s.rep.Failed++
			s.c.warn(s.rep, "read %s: not returned", ref)
			s.unread = append(s.unread, ref)
			continue
		}
		snap.Ref = ref
		facts = append(facts, snap)
	}
	if err := s.c.readThreads(ctx, facts, s.resolved.Viewer, s.rep); err != nil {
		return nil, err
	}
	return facts, nil
}

// refused tells a per-ref error GitHub will repeat: the pull request is gone
// or the token may not see it.
func refused(err error) bool {
	if api.Transient(err) {
		return false
	}
	msg := err.Error()
	return notFound(err) || strings.Contains(msg, "SAML") || strings.Contains(msg, "FORBIDDEN") || strings.Contains(msg, "Resource not accessible")
}

func (s *source) Viewer(ctx context.Context) (string, error) { return s.c.GH.Viewer(ctx) }

// readFacts are the snapshots of a full read.
type readFacts []api.Snapshot

// portStub is st as a pass compares it: its version, and the light facts it
// carries beyond it, the review requests, the review decision and the
// mergeable state, stored without a full read.
func portStub(st api.Stub) stub {
	return stub{object: st.Ref.String(), version: stubVersion(st), light: &st}
}

// stubVersion and rowVersion render what moves a pull request enough to read
// it in full; mergeable, review requests and the decision are light facts.
func stubVersion(st api.Stub) string {
	names := make([]string, len(st.FailedChecks))
	for i, n := range st.FailedChecks {
		names[i] = failedName(n)
	}
	return version(st.UpdatedAt, st.State, st.IsDraft, st.HeadOid, st.ChecksState, names)
}

func rowVersion(p pr.PR) string {
	names := make([]string, len(p.FailingChecks))
	for i, c := range p.FailingChecks {
		names[i] = c.Name
	}
	return version(p.UpdatedAt, p.State, p.IsDraft, p.HeadOid, p.ChecksState, names)
}

func version(updated time.Time, state string, draft bool, head, checks string, failed []string) string {
	slices.Sort(failed)
	d := ""
	if draft {
		d = "draft"
	}
	// failedName makes a name one line, so a line break separates names.
	return strings.Join([]string{updated.UTC().Format(time.RFC3339), state, d, head, checks, strings.Join(failed, "\n")}, "|")
}

func refsOf(objects []string) ([]api.Ref, error) {
	refs := make([]api.Ref, len(objects))
	for i, o := range objects {
		ref, err := pr.ParseKey(o)
		if err != nil {
			return nil, err
		}
		refs[i] = ref
	}
	return refs, nil
}

func objectsOf(refs []api.Ref) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.String()
	}
	return out
}

// sourceStore is the GitHub source's persistence over the inventory, for one
// fast run.
type sourceStore struct {
	c        *Collector
	resolved scope.Resolved
	rep      *Report
	versions map[string]string // the watched rows' versions, read once per run
	seen     map[string]bool   // objects Save already stamped as seen
	budget   string            // the budget Save stored
	now      time.Time
}

// Watched are the stored open pull requests something follows: a scope
// reason, a track, a workspace or a recent review turn. One whose detail is
// absent, of another version or older than DetailMaxAge has Version "",
// which reads it in full.
func (s *sourceStore) Watched(ctx context.Context) ([]stub, error) {
	open, err := s.c.followedOpen(ctx, s.now)
	if err != nil {
		return nil, err
	}
	refs := make([]api.Ref, len(open))
	for i, p := range open {
		refs[i] = p.Ref
	}
	stamps, err := s.c.Store.DetailStamps(ctx, refs)
	if err != nil {
		return nil, err
	}
	s.versions = map[string]string{}
	maxAge := s.c.DetailMaxAge()
	out := make([]stub, 0, len(open))
	for _, p := range open {
		st := stub{object: p.Ref.String(), version: rowVersion(p)}
		s.versions[st.object] = st.version
		if d, ok := stamps[p.Ref]; !ok || !d.Fresh(s.now, maxAge) {
			st.version = ""
		}
		out = append(out, st)
	}
	return out, nil
}

// DetailMaxAge is how old a stored detail may grow before a fast run reads
// its pull request again: the slow interval.
func (c *Collector) DetailMaxAge() time.Duration { return c.cfg().Slow }

func (s *sourceStore) Versions(ctx context.Context, objects []string) (map[string]string, error) {
	out := make(map[string]string, len(objects))
	var missing []api.Ref
	for _, o := range objects {
		if v, ok := s.versions[o]; ok {
			out[o] = v
			continue
		}
		ref, err := pr.ParseKey(o)
		if err != nil {
			return nil, err
		}
		missing = append(missing, ref)
	}
	if len(missing) == 0 {
		return out, nil
	}
	stored, err := s.c.Store.PRs(ctx, missing)
	if err != nil {
		return nil, err
	}
	for ref, p := range stored {
		out[ref.String()] = rowVersion(p)
	}
	return out, nil
}

func (s *sourceStore) Cursor(ctx context.Context) (string, error) {
	return s.c.Store.Meta(ctx, metaFastAt)
}

// Save stores the read snapshots, then the light facts of every other stub,
// then the cursor with the client's budget in one meta write.
func (s *sourceStore) Save(ctx context.Context, light []api.Stub, snaps readFacts, cursor string, at time.Time) error {
	if err := s.c.save(ctx, snaps, s.resolved, at, s.rep); err != nil {
		return fmt.Errorf("store pull requests: %w", err)
	}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	for _, snap := range snaps {
		s.seen[snap.Ref.String()] = true
	}
	var stubs []api.Stub
	var touched []store.Stub
	for _, st := range light {
		if s.seen[st.Ref.String()] {
			continue
		}
		stubs = append(stubs, st)
		touched = append(touched, storeStub(st))
	}
	if err := s.c.Store.TouchStubs(ctx, touched, at); err != nil {
		return err
	}
	for _, st := range stubs {
		s.seen[st.Ref.String()] = true
	}
	kv, budget, err := s.c.budgetMeta()
	if err != nil {
		return err
	}
	if cursor != "" {
		kv[metaFastAt] = cursor
	}
	if err := s.c.Store.SetMetas(ctx, kv); err != nil {
		return err
	}
	s.budget = budget
	return nil
}

// See stamps the objects Save did not already stamp.
func (s *sourceStore) See(ctx context.Context, objects []string, at time.Time) error {
	var rest []string
	for _, o := range objects {
		if !s.seen[o] {
			rest = append(rest, o)
		}
	}
	refs, err := refsOf(rest)
	if err != nil {
		return err
	}
	return s.c.Store.SeeRefs(ctx, refs, at)
}

// registration is the fast run's GitHub source and store.
func (c *Collector) registration(resolved scope.Resolved, now time.Time, rep *Report) (*source, *sourceStore) {
	return &source{c: c, resolved: resolved, now: now, rep: rep}, &sourceStore{c: c, resolved: resolved, rep: rep, now: now}
}

// storeStub is st as the store refreshes its light facts.
func storeStub(st api.Stub) store.Stub {
	return store.Stub{Ref: st.Ref, State: st.State, Mergeable: st.Mergeable, ReviewRequests: st.ReviewRequests, ReviewDecision: st.ReviewDecision}
}

// budgetMeta renders the client's budget as its meta row; budget is "" when
// the client knows none.
func (c *Collector) budgetMeta() (kv map[string]string, budget string, err error) {
	kv = map[string]string{}
	limits := c.GH.Budget()
	if len(limits) == 0 {
		return kv, "", nil
	}
	sorted := slices.SortedFunc(maps.Values(limits), func(a, b api.Limit) int { return strings.Compare(string(a.Resource), string(b.Resource)) })
	b, err := json.Marshal(sorted)
	if err != nil {
		return nil, "", err
	}
	kv[metaBudget] = string(b)
	return kv, string(b), nil
}
