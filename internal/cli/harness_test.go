package cli

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/plugins/github/api/apitest"
	ghpr "github.com/wloophq/wloop/plugins/github/pr"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	factsgithub "github.com/wloophq/wloop/internal/adapter/facts/github"
	"github.com/wloophq/wloop/internal/adapter/gitcheck"
	"github.com/wloophq/wloop/internal/adapter/source/github/present"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/adapter/view/testsink"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/harness"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/daemon"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/gitx"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

var testNow = time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)

// fakeGH holds the pull requests of a test as canned payloads, in the shapes
// of the reads the sweep made before it planned from the inventory, and
// answers a full read of any of them; see inventoryNodes.
type fakeGH struct {
	mu sync.Mutex
	// reviewer holds pull requests under review, as aliases pr0, pr1...
	reviewer string
	// author holds my pull requests, open and recently closed.
	author string
	// branch answers the branch searches.
	branch string
	basics string
	// requested holds the pull requests that request my review.
	requested string
	// linked holds pull requests a workspace links, as aliases pr0, pr1...
	linked string
	// hosted answers the collector's searches from the payloads; otherwise
	// they find nothing. Stub reads by reference always answer.
	hosted bool
	calls  []string
	// viewerErr fails the viewer query.
	viewerErr error
	// loop, when set, is a daemon's loop mutex: underLock lists the calls
	// made while it is held.
	loop      *lock.Mutex
	underLock []string
	prView    string // answers gh pr view; empty fails it
	prViewDir string // the directory gh pr view ran in
	// clone answers gh repo clone <owner/name> <path>; nil fails it.
	clone func(repo, path string) error
}

func (f *fakeGH) RunIn(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if len(args) >= 2 && args[0] == "pr" && args[1] == "view" {
		f.mu.Lock()
		f.prViewDir = dir
		f.mu.Unlock()
	}
	return f.Run(ctx, args...)
}

func (f *fakeGH) Run(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	loop := f.loop
	f.mu.Unlock()
	held := loop != nil && !loop.TryLock()
	if loop != nil && !held {
		loop.Unlock()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(args, " "))
	if held {
		f.underLock = append(f.underLock, strings.Join(args, " "))
	}
	if len(args) >= 2 && args[0] == "api" && args[1] == "graphql" {
		q := args[len(args)-1]
		out, err := f.graphql(q)
		if err != nil || strings.Contains(q, "__typename") {
			return out, err
		}
		return withoutTypenames(out)
	}
	if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
		return nil, nil
	}
	if len(args) == 4 && args[0] == "repo" && args[1] == "clone" && f.clone != nil {
		return nil, f.clone(args[2], args[3])
	}
	if len(args) >= 2 && args[0] == "pr" && args[1] == "view" {
		if f.prView == "" {
			return nil, fmt.Errorf("no pull requests found for branch")
		}
		return []byte(f.prView), nil
	}
	return nil, fmt.Errorf("fakeGH: unexpected gh %s", strings.Join(args, " "))
}

// withoutTypenames drops what GitHub leaves out of the answer to a query that
// selects no __typename.
func withoutTypenames(out []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		return out, nil
	}
	var strip func(any)
	strip = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			delete(v, "__typename")
			for _, c := range v {
				strip(c)
			}
		case []any:
			for _, c := range v {
				strip(c)
			}
		}
	}
	strip(v)
	return json.Marshal(v)
}

func (f *fakeGH) graphql(q string) ([]byte, error) {
	switch {
	case strings.Contains(q, "mutation"):
		return nil, fmt.Errorf("unexpected mutation")
	case strings.Contains(q, "...stub"):
		return f.stubReads(q)
	case strings.Contains(q, "...inv"), strings.Contains(q, "reviewThreads(last:"):
		return f.fullReads(q)
	case strings.Contains(q, "head:"):
		return []byte(f.branch), nil
	case strings.Contains(q, "pr: repository"):
		return []byte(f.basics), nil
	case strings.Contains(q, "viewer"):
		if f.viewerErr != nil {
			return nil, f.viewerErr
		}
		return []byte(`{"data":{"viewer":{"login":"me"}}}`), nil
	}
	return nil, fmt.Errorf("fakeGH: unexpected query %s", q)
}

var (
	byRefAlias  = regexp.MustCompile(`(pr\d+): repository\(owner: "([^"]+)", name: "([^"]+)"\) \{ pullRequest\(number: (\d+)\) \{ (\.\.\.inv|\.\.\.stub|reviewThreads)`)
	searchAlias = regexp.MustCompile(`(?m)^\s*(?:(s\d+): )?search\(query: ("(?:[^"\\]|\\.)*")`)
)

// stubReads answers the collector's searches and stub reads by reference
// from inventoryNodes. A search of a hosted fake returns the pull requests its is:open or
// is:closed, author: and user-review-requested: terms select, and nothing
// without one of the last two.
func (f *fakeGH) stubReads(q string) ([]byte, error) {
	_, prs, order := f.inventoryNodes()
	data := map[string]any{"rateLimit": map[string]any{"cost": 1, "remaining": 4999, "limit": 5000, "resetAt": "2026-10-09T16:00:00Z"}}
	for _, m := range searchAlias.FindAllStringSubmatch(q, -1) {
		alias := cmp.Or(m[1], "search")
		var query string
		_ = json.Unmarshal([]byte(m[2]), &query)
		found := []any{}
		for _, key := range order {
			if n := prs[key]; f.hosted && matchesSearch(n, query) {
				found = append(found, n)
			}
		}
		data[alias] = map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": found}
	}
	for _, m := range byRefAlias.FindAllStringSubmatch(q, -1) {
		if n, ok := prs[fmt.Sprintf("%s/%s#%s", m[2], m[3], m[4])]; ok {
			data[m[1]] = obj{"pullRequest": n}
		} else {
			data[m[1]] = nil
		}
	}
	return json.Marshal(obj{"data": data})
}

func matchesSearch(n map[string]any, query string) bool {
	login := func(v any) string {
		a, _ := v.(map[string]any)
		l, _ := a["login"].(string)
		return l
	}
	selected := false
	for _, term := range strings.Fields(query) {
		k, v, _ := strings.Cut(term, ":")
		switch {
		case term == "is:open" && n["state"] != dgh.StateOpen, term == "is:closed" && n["state"] == dgh.StateOpen:
			return false
		case k == "author":
			if !strings.EqualFold(login(n["author"]), v) {
				return false
			}
			selected = true
		case k == "user-review-requested":
			requests, _ := n["reviewRequests"].(map[string]any)
			nodes, _ := requests["nodes"].([]any)
			if !slices.ContainsFunc(nodes, func(r any) bool {
				rr, _ := r.(map[string]any)
				return strings.EqualFold(login(rr["requestedReviewer"]), v)
			}) {
				return false
			}
			selected = true
		}
	}
	return selected
}

// fullReads answers the full reads and the review thread reads of a query
// from inventoryNodes.
func (f *fakeGH) fullReads(q string) ([]byte, error) {
	_, prs, _ := f.inventoryNodes()
	data := map[string]any{}
	for _, m := range byRefAlias.FindAllStringSubmatch(q, -1) {
		n, ok := prs[fmt.Sprintf("%s/%s#%s", m[2], m[3], m[4])]
		switch {
		case !ok:
			data[m[1]] = nil
		case m[5] == "reviewThreads":
			data[m[1]] = obj{"pullRequest": obj{"reviewThreads": n["reviewThreads"]}}
		default:
			read := maps.Clone(n)
			if threads, ok := n["reviewThreads"].(map[string]any); ok {
				total := threads["totalCount"]
				if total == nil {
					nodes, _ := threads["nodes"].([]any)
					total = len(nodes)
				}
				read["reviewThreads"] = obj{"totalCount": total}
			}
			data[m[1]] = obj{"pullRequest": read}
		}
	}
	return json.Marshal(obj{"data": data})
}

// inventoryNodes merges the pull requests of the canned payloads by
// "owner/repo#n", in the order basics, reviewer, requested, author, linked,
// a later payload adding to and replacing the fields of an earlier one. A
// requested pull request requests the viewer's review, an authored one is the
// viewer's, and one without dates was created and updated in payload order
// before testNow. order lists the keys as first seen.
func (f *fakeGH) inventoryNodes() (viewer string, prs map[string]map[string]any, order []string) {
	viewer, prs = "me", map[string]map[string]any{}
	add := func(raw any, edit func(map[string]any)) {
		n, ok := raw.(map[string]any)
		if !ok {
			return
		}
		repo, _ := n["repository"].(map[string]any)
		name, _ := repo["nameWithOwner"].(string)
		number, _ := n["number"].(float64)
		if name == "" || number == 0 {
			return
		}
		key := fmt.Sprintf("%s#%d", name, int(number))
		pr, seen := prs[key]
		if !seen {
			pr = map[string]any{}
			prs[key], order = pr, append(order, key)
		}
		maps.Copy(pr, n)
		if edit != nil {
			edit(pr)
		}
	}
	decode := func(payload string) map[string]any {
		var v struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal([]byte(payload), &v)
		if who, ok := v.Data["viewer"].(map[string]any); ok && who["login"] != nil {
			viewer, _ = who["login"].(string)
		}
		return v.Data
	}
	byAlias := func(data map[string]any, edit func(map[string]any)) {
		aliases := slices.Sorted(maps.Keys(data))
		slices.SortStableFunc(aliases, func(a, b string) int { return len(a) - len(b) })
		for _, alias := range aliases {
			if repo, ok := data[alias].(map[string]any); ok && strings.HasPrefix(alias, "pr") {
				add(repo["pullRequest"], edit)
			}
		}
	}
	nodesOf := func(data map[string]any, alias string, edit func(map[string]any)) {
		list, _ := data[alias].(map[string]any)
		nodes, _ := list["nodes"].([]any)
		for _, n := range nodes {
			add(n, edit)
		}
	}
	if repo, ok := decode(f.basics)["pr"].(map[string]any); ok {
		add(repo["pullRequest"], nil)
	}
	byAlias(decode(f.reviewer), nil)
	nodesOf(decode(f.requested), "requested", func(pr map[string]any) {
		requests, _ := pr["reviewRequests"].(map[string]any)
		nodes, _ := requests["nodes"].([]any)
		pr["reviewRequests"] = obj{"nodes": append(slices.Clone(nodes), obj{"requestedReviewer": obj{"login": viewer}})}
	})
	author := decode(f.author)
	mine := func(pr map[string]any) { pr["author"] = obj{"login": viewer} }
	nodesOf(author, "open", mine)
	nodesOf(author, "recent", mine)
	byAlias(decode(f.linked), nil)
	for i, key := range order {
		at := testNow.Add(time.Duration(i-len(order)) * time.Minute).UTC().Format(time.RFC3339)
		if prs[key]["createdAt"] == nil {
			prs[key]["createdAt"] = at
		}
		if prs[key]["updatedAt"] == nil {
			prs[key]["updatedAt"] = at
		}
	}
	return viewer, prs, order
}

// syncedStore is the inventory a test's sweep plans from: before a plan
// reads it, which starts with the viewer, the collector reads every pull
// request the fake GitHub holds into it, as the fast job would have.
// syncedInputs are te's inventory once the collector read te's fake GitHub
// into it.
type syncedInputs struct{ te *testEnv }

func (s syncedInputs) Viewer(ctx context.Context) (string, error) {
	if err := s.te.syncInventory(ctx); err != nil {
		return "", err
	}
	return s.te.Env.reportedInputs().Viewer(ctx)
}

func (s syncedInputs) Rows(ctx context.Context) ([]dinv.PR, error) {
	return s.te.Env.reportedInputs().Rows(ctx)
}

func (s syncedInputs) Facts(ctx context.Context, rows []dinv.PR, viewer string) (map[unit.Unit]dinv.PlanFacts, error) {
	return s.te.Env.reportedInputs().Facts(ctx, rows, viewer)
}

func (s syncedInputs) Ended(ctx context.Context, kind string, units []unit.Unit) (map[unit.Unit]bool, error) {
	return s.te.Env.reportedInputs().Ended(ctx, kind, units)
}

func (s syncedInputs) PR(ctx context.Context, ref pr.Ref) (dinv.PR, bool, error) {
	return s.te.Env.reportedInputs().PR(ctx, ref)
}

// fakeGit serves read commands from canned values and records the rest.
type fakeGit struct {
	mu    sync.Mutex
	dirty map[string]bool
	head  map[string]string
	// branch is the current branch per directory; branches are the local
	// branch names that already exist.
	branch   map[string]string
	branches map[string]bool
	// ancestor marks the directories whose HEAD is an ancestor of any ref.
	ancestor map[string]bool
	// upstream is the remote branch each directory tracks.
	upstream map[string]string
	// untracked and unpushed count untracked files and commits on no remote
	// branch per directory.
	untracked, unpushed map[string]int
	// ignored lists ignored paths per directory; fetchFails fails fetches.
	ignored    map[string][]string
	fetchFails map[string]bool
	// onRemote lists the branches ls-remote --heads finds.
	onRemote map[string]bool
	// tips is the commit of each local branch rev-parse resolves, shared with
	// the fake manager; tipErr fails that read. worktrees maps a branch to the
	// git worktree that has it checked out; objects are the commits present.
	tips      map[string]string
	tipErr    error
	worktrees map[string]string
	objects   map[string]bool
	writes    []string
	// toplevel answers rev-parse --show-toplevel; "" fails it, as outside a worktree.
	toplevel string
	onRun    func(cmd string)
	// readOnly fails the test on any write.
	readOnly *testing.T
}

func (f *fakeGit) Run(ctx context.Context, dir string, _ []string, args ...string) ([]byte, error) {
	if f.onRun != nil {
		f.onRun(args[0])
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch args[0] {
	case "status":
		var out string
		if f.dirty[dir] {
			out = " M file.go\n"
		}
		if !slices.Contains(args, "--untracked-files=no") {
			out += strings.Repeat("?? new.txt\n", f.untracked[dir])
		}
		if slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "--ignored") }) {
			for _, p := range f.ignored[dir] {
				out += "!! " + p + "\n"
			}
		}
		if slices.Contains(args, "-z") {
			out = strings.ReplaceAll(out, "\n", "\x00")
		}
		return []byte(out), nil
	case "fetch":
		if f.fetchFails[dir] {
			return nil, &gitx.Error{Args: args, Code: 128, Msg: "could not read from remote repository"}
		}
	case "rev-list":
		if slices.Contains(args, "--count") {
			return []byte(strconv.Itoa(f.unpushed[dir]) + "\n"), nil
		}
		return []byte(strings.Repeat("c0ffee\n", f.unpushed[dir])), nil
	case "remote":
		return []byte("origin\n"), nil
	case "symbolic-ref":
		return []byte(f.branch[dir] + "\n"), nil
	case "show-ref":
		if b, ok := strings.CutPrefix(args[len(args)-1], "refs/remotes/origin/"); ok {
			if f.onRemote[b] {
				return nil, nil
			}
			return nil, &gitx.Error{Args: args, Code: 1}
		}
		if f.branches[strings.TrimPrefix(args[len(args)-1], "refs/heads/")] {
			return nil, nil
		}
		return nil, &gitx.Error{Args: args, Code: 1}
	case "worktree":
		if args[1] != "list" {
			break
		}
		var out string
		for b, path := range f.worktrees {
			out += "worktree " + path + "\nHEAD c0ffee\nbranch refs/heads/" + b + "\n\n"
		}
		return []byte(out), nil
	case "update-ref":
		f.tips[strings.TrimPrefix(args[1], "refs/heads/")] = args[2]
	case "rev-parse":
		if args[len(args)-1] == "--show-toplevel" {
			if f.toplevel == "" {
				return nil, &gitx.Error{Args: args, Code: 128, Msg: "not a git repository"}
			}
			return []byte(f.toplevel + "\n"), nil
		}
		if args[len(args)-1] == "refs/stash" {
			return nil, &gitx.Error{Args: args, Code: 1}
		}
		if ref, ok := strings.CutPrefix(args[len(args)-1], "refs/heads/"); ok {
			if f.tipErr != nil {
				return nil, f.tipErr
			}
			if tip, ok := f.tips[strings.TrimSuffix(ref, "^{commit}")]; ok {
				return []byte(tip + "\n"), nil
			}
			return nil, &gitx.Error{Args: args, Code: 1}
		}
		if oid, ok := strings.CutSuffix(args[len(args)-1], "^{commit}"); ok {
			if f.objects[oid] {
				return []byte(oid + "\n"), nil
			}
			return nil, &gitx.Error{Args: args, Code: 1}
		}
		if args[len(args)-1] == "@{upstream}" {
			if b, ok := f.upstream[dir]; ok {
				return []byte("refs/remotes/origin/" + b + "\n"), nil
			}
			return nil, &gitx.Error{Args: args, Code: 128}
		}
		return []byte(f.head[dir] + "\n"), nil
	case "merge-base":
		if f.ancestor[dir] {
			return nil, nil
		}
		return nil, &gitx.Error{Args: args, Code: 1}
	case "ls-remote":
		if b := strings.TrimPrefix(args[len(args)-1], "refs/heads/"); slices.Contains(args, "--heads") && f.onRemote[b] {
			return []byte("c0ffee\trefs/heads/" + b + "\n"), nil
		}
		return nil, nil
	}
	if f.readOnly != nil {
		f.readOnly.Errorf("git write under dry-run: git -C %s %s", dir, strings.Join(args, " "))
	}
	f.writes = append(f.writes, dir+": "+strings.Join(args, " "))
	return nil, nil
}

type testEnv struct {
	*Env
	t        *testing.T
	out, err *bytes.Buffer
	gh       *fakeGH
	git      *fakeGit
	cfg      config.Config
	// db is the store's database; repo holds the turns and the views' state;
	// sink is the views' board and reg the sessions' registry.
	db   *sqlite.DB
	repo *sqlite.Store
	sink *testsink.Sink
	reg  *testRegistry
	// loop is the loop mutex of the daemon serve started.
	loop *lock.Mutex
	// served ends every context serving gave.
	served     context.Context
	stopServed context.CancelFunc
}

// testRegistry knows every stored session as having the loop's MCP tool but
// those noMCP names by id or by the handle of their process.
type testRegistry struct {
	turns.Registry
	turns.Handles
	mu    sync.Mutex
	noMCP map[string]bool
}

func (r *testRegistry) withoutMCP(idOrHandle string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.noMCP[idOrHandle] = true
}

func (r *testRegistry) Session(ctx context.Context, id string) (dturn.Session, bool, error) {
	s, ok, err := r.Registry.Session(ctx, id)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil || !ok || r.noMCP[id] || s.Handle != "" && r.noMCP[s.Handle] {
		return s, ok, err
	}
	if !s.Capable() {
		s.ID, s.MCPAt = id, cmp.Or(s.StartedAt, testNow)
	}
	return s, true, nil
}

// runViews runs the views' workers until the test ends.
func (te *testEnv) runViews(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { te.Views.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func newTestEnv(t *testing.T, ws workspace.Manager) *testEnv {
	t.Helper()
	te := &testEnv{
		t:   t,
		out: &bytes.Buffer{},
		err: &bytes.Buffer{},
		gh:  &fakeGH{},
		git: &fakeGit{dirty: map[string]bool{}, head: map[string]string{}, branch: map[string]string{}, branches: map[string]bool{}, ancestor: map[string]bool{}, upstream: map[string]string{},
			untracked: map[string]int{}, unpushed: map[string]int{}, ignored: map[string][]string{}, fetchFails: map[string]bool{}, onRemote: map[string]bool{}, tips: map[string]string{}, worktrees: map[string]string{}, objects: map[string]bool{}},
		cfg: (&Env{}).defaultConfig(),
	}
	te.Env = &Env{
		effectProblems: &effectProblems{},
		background:     &sync.WaitGroup{},
		Out:            te.out,
		Err:            te.err,
		budget:         &githubBudget{},
		Git:            gitx.Git{R: te.git},
		Files:          checkoutFiles{ws},
		WS:             workspace.NewSet(ws),
		Sh:             func(context.Context, string, ...string) ([]byte, error) { return nil, fmt.Errorf("no shell in tests") },
		Stdout:         func(context.Context, string, ...string) ([]byte, error) { return nil, fmt.Errorf("no shell in tests") },
		LoadConfig:     func() (config.Config, error) { return te.cfg, nil },
		ConfigPath:     t.TempDir() + "/config.toml",
		StateDir:       t.TempDir(),
		Home:           t.TempDir(),
		Now:            func() time.Time { return testNow },
		Getenv:         func(string) string { return "" },

		ViewDebounce: 10 * time.Millisecond,
		Harness:      harnessOf(map[string]harness.Protocol{"claude": claude.Protocol{}}, defaultManagers()),

		NativeAvailable: func() bool { return false },
		Exit:            func(code int) { t.Errorf("unexpected exit %d", code) },
		Reexec:          func() error { t.Error("unexpected reexec"); return fmt.Errorf("no reexec in tests") },
		Homes:           testHomeKinds(t),
		Agents: map[string]turns.Agent{"claude": claude.Agent{Sh: func(context.Context, string, []string, string, string, ...string) ([]byte, error) {
			t.Error("unexpected headless agent call")
			return nil, fmt.Errorf("no agent in tests")
		}, AskSh: func(_ context.Context, _ string, _ []string, _, _ string, args ...string) ([]byte, error) {
			if slices.Equal(args, []string{"--help"}) {
				return []byte("  --restricted\n"), nil
			}
			t.Error("unexpected headless agent call")
			return nil, fmt.Errorf("no agent in tests")
		}}},
	}
	te.Env.forge = testForge{te}
	te.served, te.stopServed = context.WithCancel(context.Background())
	te.Env.intents = newEnvIntents(te.Env)
	te.Env.planReads = syncedInputs{te}
	te.Env.payloadPlanner = func() (psource.PayloadPlanner, error) { return te.liveGitHubPlugin() }
	te.Env.briefReader = func() (briefReader, error) { return te.liveGitHubPlugin() }
	te.Env.pluginEffects = func(p unit.ProviderID) (effectApplier, bool) {
		if p != unit.GitHub {
			return nil, false
		}
		return te.githubPluginOf(), true
	}
	if ws != nil {
		te.cfg.WorkspaceManagers = slices.Compact([]string{ws.Name(), "orca"})
	}
	if m, ok := ws.(*fake.Manager); ok && m.Tips == nil {
		m.Tips = te.git.tips
	}
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	te.db = db
	te.Rounds = sqlite.NewRounds(db)
	te.Store = inventory.New(db)
	te.Env.reports = func(st *inventory.Store) readmodel.Reports {
		derived := ghreporttest.Inventory{Store: st, Now: func() time.Time { return te.Env.Now() }, Config: func() config.Config { return te.cfg },
			Plan: &ghreporttest.InventoryPlan{Facts: storeInputs{st}, Rounds: te.Rounds, Books: te.repo, Snapshot: liveSnapshot{te.Env},
				Git: ghreporttest.GitOf(factsgithub.LocalGit(te.Env.Git), te.Env.Git)}}
		return layeredReports{kept: psource.NewSnapshots(db.Snapshots()), derived: derived}
	}
	te.repo, te.sink = sqlite.New(db), testsink.New("test")
	te.reg = &testRegistry{Registry: te.repo, Handles: te.repo, noMCP: map[string]bool{}}
	clock := clockFunc(func() time.Time { return te.Env.Now() })
	te.Views = &views.Service{Books: te.repo, Intents: registryPort{te.Env}, State: te.repo, Sinks: []views.Sink{{ViewSink: te.sink, Placements: te.sink}}, Clock: clock, Paused: te.boardPaused}
	te.Turns = &turns.Service{Intents: registryPort{te.Env}, Notices: profileNotices{te.Env}, Store: te.repo, Registry: te.reg, Sessions: turnstest.NewSessions(), Launcher: cliLauncher{te.Env},
		Answers: hostAnswers{events: sqlite.New(db), viewer: te.Store}, Changed: te.Views.Changed, Clock: clock, Command: brand.Name, FastJob: 2 * time.Minute}
	var minted atomic.Int64
	te.Turns.NewID = func() string { return fmt.Sprintf("sid-%d", minted.Add(1)) }
	return te
}

// serving is ctx for the work serve starts on te's Env, ended by
// stopServing, which the test's cleanup calls before the stores and
// directories opened so far go away.
func (te *testEnv) serving(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancel(ctx)
	context.AfterFunc(te.served, cancel)
	te.t.Cleanup(te.stopServing)
	return ctx
}

// stopServing cancels every context serving gave and waits for the work on
// them.
func (te *testEnv) stopServing() {
	te.stopServed()
	te.Env.background.Wait()
}

// serveDeps is serveDeps on te's Env, on a context serving binds to the test.
func (te *testEnv) serveDeps(ctx context.Context, db *sqlite.DB, cfg config.Config, logf func(string, ...any), loopMu *lock.Mutex, app *daemon.AppState) daemon.Deps {
	return serveDeps(te.serving(ctx), te.Env, db, cfg, logf, loopMu, app)
}

// loadRounds reads the round state the commands saved.
func (te *testEnv) loadRounds(t *testing.T) *round.Store {
	t.Helper()
	st, err := te.Rounds.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (te *testEnv) saveRounds(t *testing.T, st *round.Store) {
	t.Helper()
	if err := te.Rounds.Save(context.Background(), st); err != nil {
		t.Fatal(err)
	}
}

func (te *testEnv) setPause(t *testing.T, p round.Pause) {
	t.Helper()
	if err := te.Rounds.SetPause(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

// run runs a command against te's daemon, which it starts on the first
// command when the test set no socket.
func (te *testEnv) run(args ...string) int {
	if te.Socket == "" {
		te.serve(te.t)
	}
	return Run(context.Background(), te.Env, args)
}

// noDaemon points te's command line at a socket nothing listens on.
// bubbled runs a test in a synctest bubble, where time passes only on a
// timer: a command that returns after 0 waited for nothing.
func bubbled(f func(*testing.T)) func(*testing.T) {
	return func(t *testing.T) { synctest.Test(t, f) }
}

func (te *testEnv) noDaemon(t *testing.T) {
	te.Socket = filepath.Join(t.TempDir(), "none.sock")
}

// quietGH answers like the fake GitHub without recording the call, so a
// derived row's read leaves what tests count alone.
type quietGH struct{ f *fakeGH }

func (q quietGH) Run(ctx context.Context, args ...string) ([]byte, error) {
	out, err := q.f.Run(ctx, args...)
	q.f.mu.Lock()
	q.f.calls = q.f.calls[:len(q.f.calls)-1]
	q.f.mu.Unlock()
	return out, err
}

// serve runs te's daemon over a socket, with the actions, the turns by hand
// and AdminAPI wired as serve wires them, and points te's command line at it.
func (te *testEnv) serve(t *testing.T) { te.serveWith(t, nil) }

// serveWithTasks serves te's daemon with the GTD entries on te's store.
func (te *testEnv) serveWithTasks(t *testing.T) {
	mods, err := buildSources(plugins, te.cfg, moduleDeps{Inventory: te.Store, Turns: te.Turns, Clock: clockFunc(te.Now)}, bundledUI)
	if err != nil {
		t.Fatal(err)
	}
	te.serveWith(t, func(d daemon.Deps) daemon.Deps { return te.serveTasks(d, te.db, te.cfg, mods, t.Logf) })
}

// serveWith serves te's daemon, its deps passed through with when set.
func (te *testEnv) serveWith(t *testing.T, with func(daemon.Deps) daemon.Deps) {
	t.Helper()
	mu := &lock.Mutex{}
	te.loop = mu
	d := withRows(daemon.Deps{Pauses: te.Rounds, Turns: te.Turns, Views: te.Views, Actions: envActions{te.Env, te.Store}, Intents: te.loadedIntents, TurnByHand: te.byHand(mu), LoopMu: mu, Facts: te.prFacts(te.cfg),
		DryRunTurn: te.dryRunTurn, Doctor: te.doctorState,
		Cfg: te.cfg, Version: "test", Now: te.Now, Identity: viewerIs("me"), Log: t.Logf}, te.Store)
	d = withGitHub(d, present.Deps{Store: te.Store, Reports: te.Env.reports(te.Store), Host: te.moduleHost(te.db)(unit.GitHub),
		Plugin: te.pluginPrepare(), View: te.harnessView})
	d = te.Env.serveMessages(context.Background(), d, te.db, te.cfg, t.Logf)
	if with != nil {
		d = with(d)
	}
	srv := daemon.New(d)
	te.Env.consequences = srv
	te.Socket = serveSocket(t, srv.Handler())
}

// checkoutFiles reads the file system, except that every workspace the
// manager lists has a .git entry: the fake git serves their paths.
type checkoutFiles struct{ ws workspace.Manager }

func (f checkoutFiles) Exists(p string) bool {
	if filepath.Base(p) == ".git" && f.ws != nil {
		if list, err := f.ws.List(context.Background()); err == nil && slices.ContainsFunc(list, func(w wsdomain.Workspace) bool { return w.Path == filepath.Dir(p) }) {
			return true
		}
	}
	return gitcheck.Files{}.Exists(p)
}

func (checkoutFiles) Link(p string) bool { return gitcheck.Files{}.Link(p) }

func (checkoutFiles) Repos(dir string) ([]string, error) {
	if !(gitcheck.Files{}).Exists(dir) {
		return nil, nil
	}
	return gitcheck.Files{}.Repos(dir)
}

func (checkoutFiles) Same(a, b string) (bool, error) { return gitcheck.Files{}.Same(a, b) }

// handleSessions serves a turnstest.Sessions as the processes of the manager
// name, each terminal's session read as the manager's handle of its process.
type handleSessions struct {
	name string
	*turnstest.Sessions
}

var _ turns.ManagerSessions = handleSessions{}

func (h handleSessions) Name() string { return h.name }

func (h handleSessions) Processes(ctx context.Context, workspaceID string) ([]turns.Process, error) {
	ts, err := h.Terminals(ctx, workspaceID)
	var out []turns.Process
	for _, t := range ts {
		out = append(out, turns.Process{Handle: t.Session, Key: t.Key, Title: t.Title, Agent: t.Agent, LastOutput: t.LastOutput})
	}
	return out, err
}

func (h handleSessions) Census(ctx context.Context) (turns.ProcessCensus, error) {
	c, err := h.Sessions.Census(ctx)
	return turns.ProcessCensus{Handles: c.Sessions, Complete: c.Complete, RuntimeID: c.RuntimeID, At: c.At}, err
}

// agentCommand is the line that starts a session of opts in worktree.
func (te *testEnv) agentCommand(ctx context.Context, cfg config.Config, worktree string, opts roundOptions) (string, error) {
	s, err := te.agentSession(ctx, cfg, wsdomain.Workspace{ID: "ws-cmd", Path: worktree, DisplayName: "cmd"}, opts)
	if err != nil {
		return "", err
	}
	a, err := te.agent(cfg.Agent)
	if err != nil {
		return "", err
	}
	return a.Command(s)
}

// pluginPrepare is the GitHub plugin's nudge prepare over te's store and
// turns, as the running plugin serves the presenter.
func (te *testEnv) pluginPrepare() func(context.Context, src.ActionRequest) (json.RawMessage, error) {
	b := &psource.Broker{ID: string(unit.GitHub), Units: psource.Units{Codecs: te.unitCodecs()}, Events: sqlite.New(te.db)}
	if te.Turns != nil {
		b.Books = func(ctx context.Context, units ...unit.Unit) (map[unit.Unit]dturn.Book, error) {
			return te.Turns.Store.Books(ctx, units...)
		}
	}
	host := b.Host()
	viewer := func(ctx context.Context) (string, error) { return te.Store.Viewer(ctx, unit.GitHub) }
	return ghreporttest.Prepare(func() src.Host { return host }, viewer, func() []string { return prrow.IgnoreOf(te.cfg) }, te.Now)
}

// liveGitHubPlugin is te's GitHub plugin while te's test runs; work the
// daemon still runs past its end reaches none.
func (te *testEnv) liveGitHubPlugin() (*src.Client, error) {
	if err := te.t.Context().Err(); err != nil {
		return nil, fmt.Errorf("%w: the test ended", src.ErrUnavailable)
	}
	return te.githubPluginOf(), nil
}

// liveSnapshot is the workspace snapshot of e's managers as they are when
// read; none while they keep no snapshot.
type liveSnapshot struct{ e *Env }

func (l liveSnapshot) Read(ctx context.Context, want wsdomain.Part) (*wsdomain.Snapshot, error) {
	if l.e.WS == nil || !l.e.WS.Snapshots() {
		return nil, nil
	}
	return l.e.WS.Read(ctx, want)
}

// storeInputs are the sweep's pull requests as the inventory stores them.
type storeInputs struct{ store *inventory.Store }

func (i storeInputs) Viewer(ctx context.Context) (string, error) {
	return i.store.Viewer(ctx, unit.GitHub)
}

func (i storeInputs) Rows(ctx context.Context) ([]dinv.PR, error) {
	return inventorytest.Of(i.store).ListPRs(ctx, false)
}

// Facts reads the details of rows in one query; a detail of another version
// gives its facts with a zero ReadAt, as the collector has yet to re-read it.
func (i storeInputs) Facts(ctx context.Context, rows []dinv.PR, viewer string) (map[unit.Unit]dinv.PlanFacts, error) {
	refs := make([]pr.Ref, len(rows))
	for n, row := range rows {
		refs[n] = row.Ref
	}
	details, err := inventorytest.Of(i.store).Details(ctx, refs)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]dinv.PlanFacts, len(rows))
	for _, row := range rows {
		d, ok := details[row.Ref]
		f := dinv.PlanFacts{Facts: inventory.FactsOf(row, d, viewer)}
		if ok && d.Version == inventory.DetailVersion {
			f.ReadAt = d.ReadAt
		}
		out[row.Ref.Unit()] = f
	}
	return out, nil
}

func (i storeInputs) Ended(ctx context.Context, kind string, units []unit.Unit) (map[unit.Unit]bool, error) {
	ended, err := i.store.Ended(ctx, kind, refsOf(units))
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]bool, len(ended))
	for ref, ok := range ended {
		out[ref.Unit()] = ok
	}
	return out, nil
}

// derivedCandidate reads a pull request the inventory lacks from the fake
// GitHub's basics, with the author the authored search names when the basics
// name none; an open one by nobody when there are no basics.
func derivedCandidate(ctx context.Context, f *fakeGH, ref pr.Ref) dgh.CandidatePage {
	c := dgh.Candidate{Ref: ref, URL: ref.URL(), State: dgh.StateOpen}
	if p, err := apitest.Runner(quietGH{f}).Basics(ctx, ghpr.Ref{Owner: ref.Owner, Repo: ref.Repo, Number: ref.Number}); err == nil {
		c.State, c.Author, c.Title, c.IsDraft = p.State, p.Author, p.Title, p.IsDraft
	}
	if c.Author == "" {
		c.Author = authored(f, ref)
	}
	return dgh.CandidatePage{Candidates: []dgh.Candidate{c}}
}

func authored(f *fakeGH, ref pr.Ref) string {
	f.mu.Lock()
	raw := f.author
	f.mu.Unlock()
	var res struct {
		Data struct {
			Open struct {
				Nodes []struct {
					Number     int
					Repository struct{ NameWithOwner string }
					Author     struct{ Login string }
				}
			}
		}
	}
	if json.Unmarshal([]byte(raw), &res) != nil {
		return ""
	}
	for _, n := range res.Data.Open.Nodes {
		if n.Number == ref.Number && strings.EqualFold(n.Repository.NameWithOwner, ref.Owner+"/"+ref.Repo) {
			return n.Author.Login
		}
	}
	return ""
}

// harnessView is the GitHub plugin's views as the harness serves them: a
// pull request read from the fake GitHub, the others the forge's.
func (te *testEnv) harnessView(ctx context.Context, req src.ViewRequest) (json.RawMessage, error) {
	if req.View == "candidate" {
		var q struct {
			Owner, Repo string
			Number      int
		}
		if err := json.Unmarshal(req.Query, &q); err != nil {
			return nil, err
		}
		return json.Marshal(derivedCandidate(ctx, te.gh, pr.Ref{Owner: q.Owner, Repo: q.Repo, Number: q.Number}))
	}
	return te.Env.forgeCalls().View(ctx, req)
}

// layeredReports are the reports a plugin the test runs kept, over those
// derived from the test's inventory.
type layeredReports struct{ kept, derived readmodel.Reports }

func (r layeredReports) Units(ctx context.Context, p unit.ProviderID) ([]unit.Unit, error) {
	a, err := r.derived.Units(ctx, p)
	if err != nil {
		return nil, err
	}
	b, err := r.kept.Units(ctx, p)
	if err != nil {
		return nil, err
	}
	for _, u := range b {
		if !slices.Contains(a, u) {
			a = append(a, u)
		}
	}
	return a, nil
}

func (r layeredReports) Reported(ctx context.Context, units []unit.Unit) (map[unit.Unit]src.Snapshot, error) {
	out, err := r.derived.Reported(ctx, units)
	if err != nil {
		return nil, err
	}
	kept, err := r.kept.Reported(ctx, units)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = map[unit.Unit]src.Snapshot{}
	}
	maps.Copy(out, kept)
	return out, nil
}
