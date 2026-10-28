package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/source/github/present"
	psource "github.com/wloophq/wloop/internal/plugins/source"

	src "github.com/wloophq/wloop/pkg/plugin/source"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"
	"github.com/wloophq/wloop/internal/web"
)

// newServer seeds a store with acme/api#7 (open, review requested from me,
// two working days ago) and acme/api#8 (merged, mine) and returns a Server
// without jobs and without a token, so Handler answers every request.
func newServer(t *testing.T) *Server {
	t.Helper()
	return newServerWith(t, func(*Deps) {})
}

// store is the inventory store a test server runs on.
func (s *Server) store() *inventory.Store { return s.deps.Records.(present.Records).Store }

// briefRoles is a fake brief queue in every role, keyed by pull request.
type briefRoles interface {
	present.Queue
	BriefHealth
	prrow.Briefs
	present.Briefs
}

// withBriefs sets every brief role of d to b; nil turns briefs off.
func withBriefs(d *Deps, b briefRoles) {
	q := present.BriefQueue{Q: b}
	d.Brief, d.BriefStatus, d.BriefAttempts = q, b, q
	if r, ok := d.Records.(present.Records); ok {
		d.RowProviders = prRows(r.Store, b)
	}
	githubOf(d).Briefs = b
}

// githubDeps are the GitHub presenter's deps by the Presenters map
// withStore set, which every copy of the Deps shares; a test sets them
// before the server's first request.
var githubDeps sync.Map

// githubTest is what a test sets of the GitHub plugin: its presenter's
// deps, and the read of a pull request about to be tracked.
type githubTest struct {
	deps present.Deps
	read atomic.Pointer[func(context.Context, unit.Unit) (port.TrackRead, error)]
}

func githubTestOf(d *Deps) *githubTest {
	g, ok := githubDeps.Load(reflect.ValueOf(d.Presenters).Pointer())
	if !ok {
		panic("githubOf: no GitHub presenter on these deps; use withStore")
	}
	return g.(*githubTest)
}

// githubOf is the GitHub presenter's deps of d.
func githubOf(d *Deps) *present.Deps { return &githubTestOf(d).deps }

// trackReads makes read the read of a pull request s tracks; nil tracks
// unread.
func (s *Server) trackReads(read func(context.Context, unit.Unit) (port.TrackRead, error)) {
	githubTestOf(&s.deps).read.Store(&read)
}

// github is the GitHub presenter's deps of s.
func (s *Server) github() *present.Deps { return githubOf(&s.deps) }

// withStore is d with every store role on st and the GitHub plugin's
// presenter over it.
func withStore(d Deps, st *inventory.Store) Deps {
	if d.Units == nil {
		d.Units = unit.NewRegistry(pr.Codec{})
	}
	if d.Forge == "" {
		d.Forge = unit.GitHub
	}
	if d.Identity != nil {
		if id, err := d.Identity(context.Background()); err == nil {
			if err := ghsource.KeepIdentity(context.Background(), st, id); err != nil {
				panic(err)
			}
		}
	}
	d.Events, d.Overrides, d.Meta, d.Records = inventory.ByUnit{Store: st}, st.DB(), st, present.Records{Store: st}
	d.RowProviders = prRows(st, nil)
	d.Repo = present.Repo
	g := &githubTest{deps: present.Deps{Store: st, Reports: ghreporttest.Inventory{Store: st, Now: st.Now}}}
	d.Presenters = map[unit.ProviderID]func(PresenterHost) Presenter{unit.GitHub: func(h PresenterHost) Presenter {
		g.deps.Host = (&psource.Broker{ID: string(unit.GitHub), Units: psource.Units{Codecs: h.Units}, Events: sqlite.New(st.DB()), Marks: st.DB(),
			Drafts: st.DB(), Overrides: st.DB()}).Host()
		return present.New(g.deps, present.Host(h))
	}}
	githubDeps.Store(reflect.ValueOf(d.Presenters).Pointer(), g)
	units, now := d.Units, d.Now
	if now == nil {
		now = time.Now
	}
	d.Track, d.Untrack = testFollows(st, func(ctx context.Context, ref github.Ref) (port.TrackRead, error) {
		if read := g.read.Load(); read != nil && *read != nil {
			return (*read)(ctx, ref.Unit())
		}
		return port.TrackRead{Unit: ref.Unit()}, nil
	}, func() *unit.Registry { return units }, func() time.Time { return now() })
	if d.Follows == nil {
		d.Follows = func(p unit.ProviderID) bool { return p == unit.GitHub }
	}
	return d
}

// prRows are the pull request rows over st with the brief queue b, as the
// github source builds them.
func prRows(st *inventory.Store, b prrow.Briefs) []func(readmodel.RowDeps) readmodel.Provider {
	return []func(readmodel.RowDeps) readmodel.Provider{func(rd readmodel.RowDeps) readmodel.Provider {
		var reports readmodel.Reports = ghreporttest.Inventory{Store: st, Now: st.Now, Config: func() config.Config { return rd.Settings().Cfg }}
		if kept, ok := keptReports.Load(st); ok {
			reports = kept.(readmodel.Reports)
		}
		return prrow.New(prrow.Source{RowDeps: rd, Reports: reports, Store: st, Briefs: b})
	}}
}

// cards are the GitHub cards and items over the reports the rows of s
// read.
func (s *Server) cards() ghsource.Cards {
	st := s.store()
	if kept, ok := keptReports.Load(st); ok {
		return ghsource.Cards{Reports: kept.(readmodel.Reports)}
	}
	return ghsource.Cards{Reports: ghreporttest.Inventory{Store: st, Now: st.Now, Config: func() config.Config { return s.deps.Cfg }}}
}

// keptReports are the reports a test kept in its store, by the store whose
// rows read them; a store absent derives them from its inventory at each
// read.
var keptReports sync.Map

// keepReports keeps the plugin's reports of st's pull requests in its
// snapshot store, as the host keeps a running plugin's, and has the rows
// over st read them there.
func keepReports(t *testing.T, st *inventory.Store) {
	t.Helper()
	snaps := psource.NewSnapshots(st.DB().Snapshots())
	if err := (ghreporttest.Inventory{Store: st, Now: st.Now}).Keep(context.Background(), snaps); err != nil {
		t.Fatal(err)
	}
	keptReports.Store(st, readmodel.Reports(snaps))
	t.Cleanup(func() { keptReports.Delete(st) })
}

// newServerWith is newServer with change applied to the Deps before New.
func newServerWith(t *testing.T, change func(*Deps)) *Server {
	t.Helper()
	return newServerLogged(t, nil, change)
}

// newServerLogged is newServerWith with the store noting its writes to log
// and the daemon reading them from it; a nil log notes nothing.
func newServerLogged(t *testing.T, log *change.Log, change func(*Deps)) *Server {
	t.Helper()
	var noter sqlite.Noter
	if log != nil {
		noter = log
	}
	stDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), noter)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stDB.Close() })
	return newServerOn(t, stDB, log, change)
}

// newServerOn is newServerLogged on the store stDB.
func newServerOn(t *testing.T, stDB *sqlite.DB, log *change.Log, change func(*Deps)) *Server {
	t.Helper()
	st := inventory.New(stDB)
	ctx := context.Background()
	now := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC) // Thursday
	st.UseClock(func() time.Time { return now })
	r7 := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	st.UpsertPR(ctx, dinv.PR{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN", HeadOid: "h2", ChecksState: "SUCCESS", ReviewRequests: []string{"me"}, Reasons: []string{"review-requested"}, URL: "https://github.com/acme/api/pull/7"})
	st.AppendEvents(ctx, []dinv.Event{
		{PR: r7, Kind: dinv.KindPush, Key: "h2", Actor: "ana", At: now.Add(-50 * time.Hour)},
		{PR: r7, Kind: dinv.KindReviewRequest, Key: "me", Actor: "ana", At: now.Add(-49 * time.Hour)},
	})
	r8 := github.Ref{Owner: "acme", Repo: "api", Number: 8}
	st.UpsertPR(ctx, dinv.PR{Ref: r8, Title: "Done", Author: "me", State: "MERGED", Reasons: []string{"author"}})
	d := withStore(Deps{Pauses: sqlite.NewRounds(stDB), Activity: stDB, Cfg: config.Defaults(), Version: "test", Now: func() time.Time { return now },
		Identity: viewerIs("me", "acme/search"),
		Log:      func(string, ...any) {}}, st)
	githubOf(&d).Explorer = openOnGitHub{}
	if log != nil {
		d.Changes = log
	}
	change(&d)
	if d.Identity != nil {
		if id, err := d.Identity(ctx); err == nil {
			if err := ghsource.KeepIdentity(ctx, st, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	s := New(d)
	s.token = ""
	stopOnCleanup(t, s)
	return s
}

// stopOnCleanup stops s's lifetime when t ends and waits for the drives it
// started. Call it after the store's and the temporary directory's cleanups
// are registered, so the drives end before those run.
func stopOnCleanup(t testing.TB, s *Server) {
	t.Cleanup(func() {
		s.stopLife()
		s.driving.Wait()
	})
}

// openOnGitHub reads every pull request the inventory lacks as an open one
// of zoe's.
type openOnGitHub struct{}

func (openOnGitHub) SearchCandidates(context.Context, string, string, int) (dgh.CandidatePage, error) {
	return dgh.CandidatePage{}, nil
}

func (openOnGitHub) SearchCandidatesEach(_ context.Context, qs []string, _ string, _ int) ([]dgh.CandidatePage, []error, error) {
	return make([]dgh.CandidatePage, len(qs)), make([]error, len(qs)), nil
}

func (openOnGitHub) CandidateByRef(_ context.Context, ref github.Ref, _ string) (dgh.CandidatePage, error) {
	return dgh.CandidatePage{Candidates: []dgh.Candidate{{Ref: ref, URL: ref.URL(), Title: "Untracked", Author: "zoe", State: dgh.StateOpen}}}, nil
}

func (openOnGitHub) IsUser(context.Context, string) (bool, error) { return true, nil }

// apiRequest is a request of a client that speaks this daemon's API.
func apiRequest(method, path string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req.Host = "127.0.0.1:47000"
	req.Header.Set(api.Header, api.Version)
	return req
}

func TestAPISkew(t *testing.T) {
	t.Parallel()
	s := newServerWith(t, func(d *Deps) { d.Restart = "launchctl kickstart -k gui/501/" + brand.Name })
	h := s.Handler()
	for _, c := range []struct {
		header string
		path   string
		want   int
	}{
		{"1", "/api/rows", http.StatusOK},
		{"4.0", "/api/rows", http.StatusUpgradeRequired},
		{"2", "/api/rows", http.StatusUpgradeRequired},
		{"", "/api/rows", http.StatusUpgradeRequired},
		{"", "/api/health", http.StatusOK},
		{"", "/api/rows?api=1", http.StatusOK},
		{"1", "/api/catalog", http.StatusOK},
		{"2", "/api/catalog", http.StatusUpgradeRequired},
		{"1", "/api/nothing/here", http.StatusNotFound},
		{"", "/api/events?api=4.0", http.StatusUpgradeRequired},
		{"", "/", http.StatusOK},
		{"HEAD", "/api/health", http.StatusOK},
	} {
		method := http.MethodGet
		if c.header == "HEAD" {
			method, c.header = http.MethodHead, ""
		}
		req := httptest.NewRequest(method, c.path, nil)
		if c.header != "" {
			req.Header.Set(api.Header, c.header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%q %s: %d", c.header, c.path, rec.Code)
		}
		if rec.Code == http.StatusUpgradeRequired && !strings.Contains(rec.Body.String(), "the daemon "+api.Version) {
			t.Errorf("refusal names no versions: %s", rec.Body)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/rows", nil)
	req.Header.Set(api.Header, "2")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body struct{ Error, Fix string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil ||
		body.Error != "this client speaks API 2 and the daemon "+api.Version+"; restart the older one" || body.Fix != "launchctl kickstart -k gui/501/"+brand.Name {
		t.Fatalf("%v %s", err, rec.Body)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/rows", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "speaks API 0 ") {
		t.Fatalf("a missing version does not read as 0: %s", rec.Body)
	}
}

func TestCockpitSpeaksTheAPIVersion(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	newServer(t).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/app.js", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `const API_VERSION = "`+api.Version+`"`) ||
		!strings.Contains(rec.Body.String(), "out.fix") {
		t.Fatalf("%d: app.js does not declare API %s", rec.Code, api.Version)
	}
}

// get serves path and decodes its answer into out. The rows of a view,
// path api.Rows with the query parameter view, are filtered here, as every
// client filters the rows it reads.
func get(t *testing.T, s *Server, path string, out any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, apiRequest("GET", path, nil))
	if out != nil && rec.Code == 200 {
		into := out
		var page api.RowsPage
		if rows, ok := out.(*[]api.Row); ok && strings.HasPrefix(path, api.Rows) {
			into = &page
			defer func() { *rows = viewOf(t, path, page.Rows) }()
		}
		if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
			t.Fatalf("%s: %v: %s", path, err, rec.Body)
		}
	}
	return rec.Code
}

func viewOf(t *testing.T, path string, rows []api.Row) []api.Row {
	t.Helper()
	u, err := url.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	view := u.Query().Get("view")
	if view == "" {
		return rows
	}
	return slices.DeleteFunc(rows, func(r api.Row) bool { return !r.In(view) })
}

func TestPRsViewNeedsMe(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	var rows []api.Row
	if code := get(t, s, "/api/rows?view=needs-me", &rows); code != 200 {
		t.Fatalf("%d", code)
	}
	if len(rows) != 1 || rows[0].Summary.WaitsOn != "me" || rows[0].Kind != "review" || !rows[0].Summary.Stalled {
		t.Fatalf("%+v", rows)
	}
	if !slices.Contains(rows[0].Views, "stalled") {
		t.Fatalf("views: %v", rows[0].Views)
	}
}

func TestAReviewToAddressWithoutAWorkspaceShowsInNeedsMeUntilARoundRuns(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	now := s.deps.Now()
	r9 := github.Ref{Owner: "acme", Repo: "api", Number: 9}
	s.store().UpsertPR(ctx, dinv.PR{Ref: r9, Title: "Mine", Author: "me", State: "OPEN", HeadOid: "h9", ChecksState: "SUCCESS", Reasons: []string{"author"}})
	s.store().AppendEvents(ctx, []dinv.Event{
		{PR: r9, Kind: dinv.KindReview, Key: "r1", Actor: "bob", At: now.Add(-2 * time.Hour), Data: "CHANGES_REQUESTED:h9"},
		{PR: r9, Kind: dinv.KindWouldAddress, Key: "2026-10-08T08:00:00Z", At: now.Add(-time.Hour), Data: "reviewed by bob"},
	})
	row := func() (api.Row, bool) {
		var rows []api.Row
		if code := get(t, s, "/api/rows?view=needs-me", &rows); code != 200 {
			t.Fatalf("%d", code)
		}
		for _, r := range rows {
			if rowNumber(r) == 9 {
				return r, true
			}
		}
		return api.Row{}, false
	}
	r, ok := row()
	if !ok || r.Kind != "to_address" || r.Summary.WaitsOn != "me" || r.Summary.Reason != "review to address, no workspace: reviewed by bob" {
		t.Fatalf("%v %+v", ok, r)
	}
	s.store().AppendEvents(ctx, []dinv.Event{{PR: r9, Kind: dinv.KindRoundSent, At: now.Add(-time.Minute), Data: "reviewed by bob"}})
	if r, ok := row(); ok && r.Kind == "to_address" {
		t.Fatalf("a round ran and it still waits: %+v", r)
	}
}

func TestPRsViewAllIncludesDone(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	var rows []api.Row
	get(t, s, "/api/rows?view=all", &rows)
	if len(rows) != 2 {
		t.Fatalf("%+v", rows)
	}
	get(t, s, "/api/rows?view=mine", &rows)
	if len(rows) != 0 {
		t.Fatalf("done rows belong to all only: %+v", rows)
	}
}

func TestDetailIncludesEvents(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	var d api.Detail
	if code := get(t, s, "/api/units/github/acme%2Fapi%237", &d); code != 200 || len(d.Events) != 2 || d.Events[1].Kind != dinv.KindReviewRequest || rowNumber(d.Row) != 7 {
		t.Fatalf("%d %+v", code, d)
	}
	if code := get(t, s, "/api/units/github/acme%2Fapi%2399", nil); code != 404 {
		t.Fatalf("%d", code)
	}
}

func TestHealthListsVersionAndWarnings(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	seedRateLimit(t, s, 100, 5000, s.deps.Now().Add(time.Hour))
	s.warnings.set("fast", "rate limit low")
	var h api.Health
	if code := get(t, s, "/api/health", &h); code != 200 || h.Version != "test" || !slices.Contains(h.Warnings, "fast: rate limit low") || !h.SlowSync {
		t.Fatalf("%d %+v", code, h)
	}
	if h.Build == "" || h.Build != web.Build("test") {
		t.Errorf("health names build %q, the cockpit it serves is %q", h.Build, web.Build("test"))
	}
}

func TestAuthHandlerRequiresToken(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	s.token = "secret"
	rec := httptest.NewRecorder()
	s.AuthHandler().ServeHTTP(rec, apiRequest("GET", "/api/health", nil))
	if rec.Code != 401 {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	req := apiRequest("GET", "/api/health", nil)
	req.AddCookie(&http.Cookie{Name: "loop_token", Value: "secret"})
	rec = httptest.NewRecorder()
	s.AuthHandler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200 with cookie, got %d", rec.Code)
	}
	req = apiRequest("POST", "/api/jobs/sweep", nil)
	req.AddCookie(&http.Cookie{Name: "loop_token", Value: "secret"})
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	s.AuthHandler().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("cross-site POST accepted: %d", rec.Code)
	}
}

func TestSSEReceivesChange(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/events?api=" + api.Version)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	if line, _ := r.ReadString('\n'); !strings.HasPrefix(line, ": connected") {
		t.Fatalf("first line %q", line)
	}
	s.hub.publish(api.Change{Type: "changed", Epoch: "e", Rev: 2})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, `"changed"`) {
			return
		}
	}
	t.Fatal("no change event")
}

func TestHealthSaysWhenNoNotifier(t *testing.T) {
	t.Parallel()
	var start = "launchctl kickstart gui/501/" + brand.LaunchAgent() + ".app"
	s := newServerWith(t, func(d *Deps) { d.NotifierStart = start })
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	health := func() api.Health {
		t.Helper()
		resp, err := http.Get(srv.URL + "/api/health?api=" + api.Version)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var h api.Health
		if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
			t.Fatal(err)
		}
		return h
	}
	var warning = "menu bar not running: notifications are off; " + start
	if h := health(); h.Notifier || !slices.Contains(h.Warnings, warning) {
		t.Fatalf("no subscriber: %+v", h)
	}
	for _, c := range []string{"tui", "app"} {
		resp, err := http.Get(srv.URL + "/api/events?client=" + c + "&api=" + api.Version)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if line, _ := bufio.NewReader(resp.Body).ReadString('\n'); !strings.HasPrefix(line, ": connected") {
			t.Fatalf("first line %q", line)
		}
		if h := health(); h.Notifier != (c == "app") || slices.Contains(h.Warnings, warning) == (c == "app") {
			t.Fatalf("%s subscribed: %+v", c, h)
		}
	}
}

func TestHealthWarnsOfNoNotifierOnlyWhenOneIsExpected(t *testing.T) {
	t.Parallel()
	var h api.Health
	if code := get(t, newServer(t), "/api/health", &h); code != 200 || h.Notifier || slices.ContainsFunc(h.Warnings, func(w string) bool { return strings.HasPrefix(w, "menu bar") }) {
		t.Fatalf("%d %+v", code, h)
	}
}

// seedRateLimit gives s a GitHub client whose GraphQL bucket is the one
// given.
func seedRateLimit(t *testing.T, s *Server, remaining, limit int, resetAt time.Time) {
	t.Helper()
	c := github.NewClient(github.Options{Host: "github.com", Now: s.deps.Now,
		Token: func(context.Context) (string, error) { return "", errors.New("no token in tests") }})
	c.Restore([]dgh.Limit{{Resource: dgh.ResourceGraphQL, Remaining: remaining, Limit: limit, ResetAt: resetAt}})
	s.deps.Budget = ghsource.Budget{Limiter: c}
	s.github().Budget = ghsource.Budget{Limiter: c}
}

func TestHealthSlowSyncFollowsTheRateLimit(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	now := s.deps.Now()
	s.warnings.set("fast", "truncated: review-requested:@me")
	var h api.Health
	get(t, s, "/api/health", &h)
	if h.SlowSync {
		t.Fatalf("slow sync without a stored rate limit: %+v", h)
	}
	seedRateLimit(t, s, 4000, 5000, now.Add(time.Hour))
	get(t, s, "/api/health", &h)
	want := api.RateLimit{Remaining: 4000, Limit: 5000, ResetAt: now.Add(time.Hour)}
	if h.SlowSync || h.RateLimit != want {
		t.Fatalf("healthy budget: %+v", h)
	}
	seedRateLimit(t, s, 100, 5000, now.Add(time.Hour))
	get(t, s, "/api/health", &h)
	if !h.SlowSync || h.RateLimit.Remaining != 100 {
		t.Fatalf("low budget: %+v", h)
	}
	seedRateLimit(t, s, 100, 5000, now.Add(-time.Minute))
	get(t, s, "/api/health", &h)
	if h.SlowSync {
		t.Fatalf("slow sync after the reset: %+v", h)
	}
}

func TestHealthSaysWhenAReviewStalls(t *testing.T) {
	t.Parallel()
	s := newServerWith(t, func(d *Deps) { d.Cfg.GitHub.Stalled.Review = config.Days(72 * time.Hour) })
	var h api.Health
	get(t, s, "/api/health", &h)
	if h.StalledReviewHours != 72 {
		t.Fatalf("%v", h.StalledReviewHours)
	}
}

func TestHealthJSONIsSnakeCase(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	seedRateLimit(t, s, 100, 5000, s.deps.Now().Add(time.Hour))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, apiRequest("GET", "/api/health", nil))
	if body := rec.Body.String(); !strings.Contains(body, `"rate_limit":{"cost":0,"remaining":100,"limit":5000,"reset_at":`) || !strings.Contains(body, `"jobs":[]`) {
		t.Fatalf("%s", body)
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, apiRequest("GET", "/api/units/github/acme%2Fapi%237", nil))
	if body := rec.Body.String(); !strings.Contains(body, `"events":[{"unit":{"provider":"github","key":"acme/api#7"}`) || !strings.Contains(body, `"kind":"push","key":"h2","actor":"ana","at":`) {
		t.Fatalf("%s", body)
	}
}

func TestSSEUnsubscribesOnDisconnect(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/events?api=" + api.Version)
	if err != nil {
		t.Fatal(err)
	}
	if line, _ := bufio.NewReader(resp.Body).ReadString('\n'); !strings.HasPrefix(line, ": connected") {
		t.Fatalf("first line %q", line)
	}
	subs := func() int {
		s.hub.mu.Lock()
		defer s.hub.mu.Unlock()
		return len(s.hub.subs)
	}
	if n := subs(); n != 1 {
		t.Fatalf("%d subscribers while connected", n)
	}
	resp.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for subs() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("subscriber left behind after disconnect")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCockpitPageIsBehindTheToken(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	s.token = "secret"
	rec := httptest.NewRecorder()
	s.AuthHandler().ServeHTTP(rec, apiRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Fatalf("without token: %d", rec.Code)
	}
	req := apiRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "loop_token", Value: "secret"})
	rec = httptest.NewRecorder()
	s.AuthHandler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `id="rail"`) {
		t.Fatalf("with cookie: %d %s", rec.Code, rec.Body)
	}
}

func TestUnknownAPIPathIsAJSONError(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, apiRequest("GET", "/api/nope", nil))
	var body map[string]string
	if rec.Code != 404 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["error"] == "" {
		t.Fatalf("%d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, apiRequest("POST", "/api/rows", nil))
	if rec.Code != 405 {
		t.Fatalf("POST /api/rows: %d", rec.Code)
	}
}

func TestHealthReportsTheViewer(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	get(t, s, "/api/rows", nil)
	var h api.Health
	if code := get(t, s, "/api/health", &h); code != 200 || h.Viewer != "me" {
		t.Fatalf("%d %+v", code, h)
	}
}

type stubBriefs struct{}

func (stubBriefs) Run(context.Context) (string, error) { return "0 briefed, 0 failed", nil }
func (stubBriefs) Status(context.Context) (brief.Status, error) {
	return brief.Status{Ready: 1, LastError: "claude is not on the PATH"}, nil
}
func (stubBriefs) Views(_ context.Context, prs ...dinv.PR) (map[github.Ref]*brief.View, error) {
	return viewsOf(&brief.View{State: "ready", Brief: &brief.Brief{Goal: "Export in batches", Kind: "feature", Size: "m", Risk: "low"}}, prs), nil
}

func (stubBriefs) Briefed(context.Context) (map[github.Ref]brief.Summary, map[github.Ref]dinv.BriefRecord, error) {
	return nil, nil, nil
}
func (stubBriefs) Forget(context.Context, github.Ref) error { return nil }
func (stubBriefs) Breaker() (bool, time.Time, string, time.Time) {
	return false, time.Time{}, "", time.Time{}
}
func (stubBriefs) Attempt(context.Context, github.Ref) (int, error) { return 1, nil }
func (stubBriefs) AttemptOf(dinv.PR, dinv.BriefRecord) int          { return 1 }
func (stubBriefs) RefreshIfStale(context.Context, dinv.PR) error    { return nil }
func (stubBriefs) Touched([]github.Ref)                             {}
func (stubBriefs) Due() <-chan struct{}                             { return nil }

type summaryBriefs struct {
	stubBriefs
	calls int
}

func (b *summaryBriefs) Briefed(context.Context) (map[github.Ref]brief.Summary, map[github.Ref]dinv.BriefRecord, error) {
	b.calls++
	return map[github.Ref]brief.Summary{
		{Owner: "acme", Repo: "api", Number: 7}: {Size: "m", Risk: "high", Goal: "Export\x1b[2J in\nbatches\u202e", OpenQuestions: 2, Tags: []string{"change:feature", "lang:go"}},
		{Owner: "acme", Repo: "web", Number: 1}: {Size: "s", Risk: "low", Goal: "not listed"},
	}, nil, nil
}

func TestRowsCarryTheBriefSummaryReadOncePerListing(t *testing.T) {
	t.Parallel()
	sb := &summaryBriefs{}
	s := newServerWith(t, func(d *Deps) { withBriefs(d, sb) })
	var rows []api.Row
	if code := get(t, s, "/api/rows?view=all", &rows); code != 200 || len(rows) < 2 {
		t.Fatalf("%d %d rows", code, len(rows))
	}
	if sb.calls != 1 {
		t.Fatalf("%d brief reads for one listing", sb.calls)
	}
	for _, r := range rows {
		switch {
		case rowNumber(r) == 7 && (payload(r).Brief == nil || !reflect.DeepEqual(*payload(r).Brief, api.BriefSummary{Size: "m", Risk: "high", Goal: "Export [2J in batches", OpenQuestions: 2, Tags: []string{"change:feature", "lang:go"}})):
			t.Fatalf("row 7 brief %+v", payload(r).Brief)
		case rowNumber(r) != 7 && payload(r).Brief != nil:
			t.Fatalf("a row without a brief has one: %+v", r)
		}
	}
	var off []api.Row
	if get(t, newServer(t), "/api/rows?view=all", &off); payload(off[0]).Brief != nil {
		t.Fatalf("briefs off: %+v", payload(off[0]).Brief)
	}
}

func TestBriefInHealthAndDetail(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	var h api.Health
	get(t, s, "/api/health", &h)
	if h.Brief != nil {
		t.Fatalf("brief in health while off: %+v", h.Brief)
	}
	s = newServerWith(t, func(d *Deps) { withBriefs(d, stubBriefs{}) })
	get(t, s, "/api/health", &h)
	if h.Brief == nil || h.Brief.Ready != 1 || !slices.Contains(h.Warnings, "brief: claude is not on the PATH") {
		t.Fatalf("health %+v", h)
	}
	var d struct {
		Brief *brief.View `json:"brief"`
	}
	get(t, s, "/api/units/github/acme%2Fapi%237", &d)
	if d.Brief == nil || d.Brief.Brief.Goal != "Export in batches" {
		t.Fatalf("detail brief %+v", d.Brief)
	}
	if _, ok := s.jobs["brief"]; !ok {
		t.Fatal("no brief job")
	}
}

type brokenBriefs struct{ stubBriefs }

func (brokenBriefs) Views(context.Context, ...dinv.PR) (map[github.Ref]*brief.View, error) {
	return nil, errors.New("database is locked")
}

func TestDetailWithoutTheBriefWhenItCannotBeRead(t *testing.T) {
	t.Parallel()
	s := newServerWith(t, func(d *Deps) { withBriefs(d, brokenBriefs{}) })
	var d struct {
		Row   api.Row     `json:"row"`
		Brief *brief.View `json:"brief"`
	}
	if code := get(t, s, "/api/units/github/acme%2Fapi%237", &d); code != 200 || d.Brief != nil || d.Row.Title != "Batch export" {
		t.Fatalf("%d %+v", code, d)
	}
}

func TestChecksDetailNamesTheFailedStepsOnce(t *testing.T) {
	t.Parallel()
	calls := 0
	s := newServerWith(t, func(d *Deps) {
		githubOf(d).FailedSteps = func(_ context.Context, owner, repo string, job int64) ([]string, error) {
			calls++
			if owner != "acme" || repo != "api" || job != 114268776435 {
				t.Errorf("asked for %s/%s job %d", owner, repo, job)
			}
			return []string{"Lint PR title"}, nil
		}
	})
	ctx := context.Background()
	pr, _, _ := s.store().PR(ctx, r7)
	pr.ChecksState = "FAILURE"
	pr.FailingChecks = []dinv.FailingCheck{
		{Name: "Build and test", URL: "https://github.com/acme/api/actions/runs/38071193224/job/114268776435"},
		{Name: "Preview site", Summary: "Build ou deploy do preview falhou"},
	}
	if err := s.store().UpsertPR(ctx, pr); err != nil {
		t.Fatal(err)
	}
	var got []api.CheckDetail
	for range 2 {
		if code := get(t, s, "/api/units/github/acme%2Fapi%237/checks", &got); code != 200 {
			t.Fatalf("%d", code)
		}
	}
	if calls != 1 || len(got) != 2 || !slices.Equal(got[0].Steps, []string{"Lint PR title"}) || got[1].Summary == "" || got[1].Steps != nil {
		t.Fatalf("calls %d, got %+v", calls, got)
	}
	if code := get(t, s, "/api/units/github/acme%2Fapi%2399/checks", nil); code != 404 {
		t.Fatalf("unknown pull request: %d", code)
	}
}

func TestChecksDetailAsksAtMostFiveJobsAndRemembersAFailureForAWhile(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var asked []string
	clock := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	s := newServerWith(t, func(d *Deps) {
		d.Now = func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return clock
		}
		githubOf(d).FailedSteps = func(_ context.Context, owner, repo string, job int64) ([]string, error) {
			mu.Lock()
			defer mu.Unlock()
			asked = append(asked, fmt.Sprintf("%s/%s %d", owner, repo, job))
			if job == 1 {
				return nil, errors.New("HTTP 403")
			}
			return []string{"step"}, nil
		}
	})
	ctx := context.Background()
	pr, _, _ := s.store().PR(ctx, r7)
	pr.ChecksState = "FAILURE"
	pr.FailingChecks = nil
	for job := 1; job <= 6; job++ {
		pr.FailingChecks = append(pr.FailingChecks, dinv.FailingCheck{Name: fmt.Sprint("job ", job), URL: fmt.Sprintf("https://github.com/acme/api-fork/actions/runs/9/job/%d", job)})
	}
	pr.FailingChecks = append(pr.FailingChecks, dinv.FailingCheck{Name: "huge", URL: "https://github.com/acme/api/actions/runs/9/job/99999999999999999999"})
	if err := s.store().UpsertPR(ctx, pr); err != nil {
		t.Fatal(err)
	}
	calls := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := slices.Clone(asked)
		slices.Sort(out)
		asked = nil
		return out
	}
	var got []api.CheckDetail
	if code := get(t, s, "/api/units/github/acme%2Fapi%237/checks", &got); code != 200 || len(got) != 7 {
		t.Fatalf("%d %+v", code, got)
	}
	if c := calls(); len(c) != 5 || c[0] != "acme/api-fork 1" {
		t.Fatalf("first load asked %v", c)
	}
	get(t, s, "/api/units/github/acme%2Fapi%237/checks", &got)
	if c := calls(); !slices.Equal(c, []string{"acme/api-fork 6"}) {
		t.Fatalf("second load asked %v: a failure or a success was asked again", c)
	}
	mu.Lock()
	clock = clock.Add(10*time.Minute + time.Second)
	mu.Unlock()
	get(t, s, "/api/units/github/acme%2Fapi%237/checks", &got)
	if c := calls(); !slices.Equal(c, []string{"acme/api-fork 1"}) {
		t.Fatalf("after the retry delay asked %v", c)
	}
}

func TestDetailNamesTheProfileChain(t *testing.T) {
	t.Parallel()
	s := newServerWith(t, func(d *Deps) {
		d.Cfg.Profile = "/u"
	})
	var d api.Detail
	get(t, s, "/api/units/github/acme%2Fapi%237", &d)
	if d.ProfileChain != "bundled + plugin:slack + user" {
		t.Fatalf("chain %q", d.ProfileChain)
	}
}

type fakeBriefs struct {
	stubBriefs
	forgot []github.Ref
}

func (f *fakeBriefs) Forget(_ context.Context, ref github.Ref) error {
	f.forgot = append(f.forgot, ref)
	return nil
}

func TestBriefRefreshActionTriggersTheJob(t *testing.T) {
	t.Parallel()
	off := newServer(t)
	if rec := post(t, off, "/api/units/github/acme%2Fapi%237/brief_refresh", `{}`); rec.Code != http.StatusConflict {
		t.Fatalf("brief off: %d %s", rec.Code, rec.Body)
	}
	fb := &fakeBriefs{}
	s := newServerWith(t, func(d *Deps) { withBriefs(d, fb) })
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/brief_refresh", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(fb.forgot) != 1 || fb.forgot[0].Number != 7 {
		t.Fatalf("forgot %v", fb.forgot)
	}
}

func TestPauseOfARepositoryHoldsOnlyItAndResumingLessThanEverythingIsRefused(t *testing.T) {
	t.Parallel()
	plan := newPendingPlan(time.Time{})
	s := newServerWith(t, func(d *Deps) { d.Plan, d.Pending = plan, plan })
	plan.propose(r7.Unit())
	if rec := post(t, s, "/api/pause", `{"paused":true,"repos":["acme/other"]}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !plan.isPending(r7.Unit()) {
		t.Fatal("pausing acme/other dropped a round of acme/api")
	}
	var h api.Health
	if get(t, s, "/api/health", &h); h.Paused {
		t.Fatal("health says everything is paused")
	}
	post(t, s, "/api/pause", `{"paused":true,"repos":["ACME/api"]}`)
	if plan.isPending(r7.Unit()) {
		t.Fatal("pausing acme/api kept its pending round")
	}
	p, err := s.deps.Pauses.Pause(context.Background())
	if err != nil || p.On || !slices.Equal(p.Repos, []string{"acme/other", "ACME/api"}) || !p.Holds("acme", "api") || p.Holds("acme", "web") {
		t.Fatalf("pause %+v %v", p, err)
	}
	post(t, s, "/api/pause", `{"paused":true}`)
	if get(t, s, "/api/health", &h); !h.Paused || !h.PausedAt.Equal(s.deps.Now()) {
		t.Fatalf("health %v %v", h.Paused, h.PausedAt)
	}
	if rec := post(t, s, "/api/pause", `{"paused":false,"repos":["acme/api"]}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "everything is paused") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	post(t, s, "/api/pause", `{"paused":false}`)
	if p, err := s.deps.Pauses.Pause(context.Background()); err != nil || p.Any() {
		t.Fatalf("after resuming everything: %+v %v", p, err)
	}
}

type countingWriter struct {
	header http.Header
	codes  []int
}

func (c *countingWriter) Header() http.Header         { return c.header }
func (c *countingWriter) Write(b []byte) (int, error) { return len(b), nil }
func (c *countingWriter) WriteHeader(code int)        { c.codes = append(c.codes, code) }

func TestWriteJSONSetsOneStatus(t *testing.T) {
	t.Parallel()
	for name, v := range map[string]any{"encodable": map[string]int{"a": 1}, "unencodable": make(chan int)} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := &countingWriter{header: http.Header{}}
			writeJSON(w, v)
			if len(w.codes) > 1 {
				t.Fatalf("statuses written: %v", w.codes)
			}
			if name == "unencodable" && (len(w.codes) != 1 || w.codes[0] != http.StatusInternalServerError) {
				t.Fatalf("statuses written: %v", w.codes)
			}
		})
	}
}

func TestHealthWarnsWhenTheConfigChanged(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	stale := false
	s.deps.ConfigWarning = func() string {
		if stale {
			return "config.toml does not load"
		}
		return ""
	}
	warned := func() bool {
		var h api.Health
		get(t, s, "/api/health", &h)
		return slices.ContainsFunc(h.Warnings, func(w string) bool { return strings.HasPrefix(w, "config: ") })
	}
	if warned() {
		t.Fatal("a fresh config warns")
	}
	stale = true
	if !warned() {
		t.Fatal("a changed config raised no warning")
	}
	stale = false
	if warned() {
		t.Fatal("the warning did not clear")
	}
}

func viewsOf(v *brief.View, prs []dinv.PR) map[github.Ref]*brief.View {
	out := map[github.Ref]*brief.View{}
	for _, pr := range prs {
		out[pr.Ref] = v
	}
	return out
}

func TestANoticeStreamsAsANotifyMessage(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/events?api=" + api.Version)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	if line, _ := r.ReadString('\n'); !strings.HasPrefix(line, ": connected") {
		t.Fatalf("first line %q", line)
	}
	s.hub.publish(api.Change{Type: "changed", Epoch: "e", Rev: 2})
	s.notify(api.Notice{ID: "n1", Workflow: "ping", Text: "look at it"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, `"look at it"`) {
			if pos := s.hub.position(); pos != (api.Since{Epoch: "e", Rev: 2}) {
				t.Fatalf("a notice moved the rows' position to %v", pos)
			}
			return
		}
	}
	t.Fatal("no notify message")
}

func TestVersion1RefusesAPreReleaseClient(t *testing.T) {
	t.Parallel()
	h := newServerWith(t, func(*Deps) {}).Handler()
	for _, path := range []string{api.Rows, api.Catalog, "/api/health"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set(api.Header, "4.0")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var body struct{ Error string }
		if path == "/api/health" {
			if rec.Code != http.StatusOK {
				t.Errorf("health: %d", rec.Code)
			}
			continue
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusUpgradeRequired ||
			body.Error != "this client speaks API 4.0 and the daemon 1; restart the older one" {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestCatalogRouteServesTheProviders(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	newServerWith(t, func(*Deps) {}).Handler().ServeHTTP(rec, apiRequest(http.MethodGet, api.Catalog, nil))
	var c api.CatalogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(c.Views) != 11 || c.Views[0].ID != api.ViewNeedsMe || len(c.Situations) != 18 || c.Version == "" {
		t.Fatalf("catalog %+v", c)
	}
}

// viewerIs is the forge's Identity of login with teams.
func viewerIs(login string, teams ...string) func(context.Context) (src.Identity, error) {
	return func(context.Context) (src.Identity, error) { return identityOf(login, teams...), nil }
}

func identityOf(login string, teams ...string) src.Identity {
	return src.Identity{Viewer: src.Person{Key: "github:" + login, Name: login}, Teams: teams}
}

// refOf is the pull request of a github unit, the zero reference for any other.
func refOf(u unit.Unit) pr.Ref {
	ref, _ := pr.RefOf(u)
	return ref
}
