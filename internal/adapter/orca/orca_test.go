package orca_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/orca"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/core/views/viewstest"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/view"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
	wsorca "github.com/wloophq/wloop/internal/workspace/orca"
)

var cols = orca.Columns{ReviewStatus: "reviewing", ReviewFallbackStatus: "in-review", NeedsMeStatus: "needs-me", ReviewedStatus: "reviewed", InProgressStatus: "in-progress", InReviewStatus: "in-review", ReadyStatus: "ready-to-merge", CompletedStatus: "completed"}

var terminalList = `{"ok":true,"result":{"terminals":[
	{"handle":"term_1","worktreeId":"repo::/wt/acme-api-7","tabId":"tab-1","title":"` + brand.Command + ` review acme/api#7","agentIdentity":"codex","connected":true,"writable":true,"orphaned":false,"lastOutputAt":1760745600000},
	{"handle":"term_2","worktreeId":"repo::/wt/acme-api-7","tabId":"tab-2","title":"zsh","agentIdentity":"","connected":true,"writable":true,"orphaned":false,"lastOutputAt":1760745000000},
	{"handle":"term_old","worktreeId":"repo::/wt/acme-api-7","tabId":"tab-4","title":"claude","agentIdentity":"claude","connected":false,"writable":false,"orphaned":true,"lastOutputAt":1760700000000}
]}}`

const worktreePs = `{"ok":true,"result":{"worktrees":[
	{"worktreeId":"repo::/wt/acme-api-7","repoId":"repo","path":"/wt/acme-api-7","isActive":false,"linkedPR":{"number":7,"state":"open"},
	 "agents":[{"paneKey":"tab-1:leaf-1","state":"done","lastAssistantMessage":"Published the review."},{"paneKey":"tab-3:leaf-1","state":"working","lastAssistantMessage":""}]},
	{"worktreeId":"repo::/wt/acme-web-9","repoId":"repo","path":"/wt/acme-web-9","isActive":true,"linkedPR":null,
	 "agents":[{"paneKey":"tab-9:leaf-2","state":"waiting"}]}
]}}`

type card struct {
	status, comment string
	unread          bool
}

// board is a canned orca.Runner that keeps card state the way the app does.
type board struct {
	mu      sync.Mutex
	columns []string
	cards   map[string]*card
	refs    map[unit.Unit]string
	typed   []turnstest.Typed
	closed  []string
	calls   []string
}

func newBoard(columns ...string) *board {
	return &board{columns: columns, cards: map[string]*card{}, refs: map[unit.Unit]string{}}
}

func (b *board) terminalList() string {
	var doc struct {
		OK     bool `json:"ok"`
		Result struct {
			Terminals []map[string]any `json:"terminals"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(terminalList), &doc); err != nil {
		panic(err)
	}
	doc.Result.Terminals = slices.DeleteFunc(doc.Result.Terminals, func(t map[string]any) bool {
		h, _ := t["handle"].(string)
		return slices.Contains(b.closed, h)
	})
	raw, _ := json.Marshal(doc)
	return string(raw)
}

func ok(result string) []byte { return []byte(`{"ok":true,"result":` + result + `}`) }

func flag(args []string, name string) (string, bool) {
	i := slices.Index(args, name)
	if i < 0 {
		return "", false
	}
	if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
		return args[i+1], true
	}
	return "", true
}

func (b *board) Run(_ context.Context, args ...string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, strings.Join(args, " "))
	switch strings.Join(args[:2], " ") {
	case "terminal list":
		return []byte(b.terminalList()), nil
	case "worktree ps":
		return []byte(worktreePs), nil
	case "terminal send":
		h, _ := flag(args, "--terminal")
		text, _ := flag(args, "--text")
		b.typed = append(b.typed, turnstest.Typed{Handle: h, Line: text})
		stages := `["input_accepted"]`
		if h == turnstest.World.Idle {
			stages = `["input_accepted","turn_started"]`
		}
		return ok(`{"send":{"handle":"` + h + `","accepted":true,"prompt":{"stages":` + stages + `}}}`), nil
	case "terminal close":
		h, _ := flag(args, "--terminal")
		b.closed = append(b.closed, h)
		return ok(`{}`), nil
	case "worktree list":
		var rows []map[string]any
		for id, c := range b.cards {
			rows = append(rows, map[string]any{"id": id, "workspaceStatus": c.status, "comment": c.comment, "isUnread": c.unread})
		}
		raw, _ := json.Marshal(map[string]any{"worktrees": rows})
		return ok(string(raw)), nil
	case "worktree set":
		id, _ := flag(args, "--worktree")
		c := b.cards[strings.TrimPrefix(id, "id:")]
		if c == nil {
			return []byte(`{"ok":false,"error":{"code":"not_found","message":"no such worktree"}}`), nil
		}
		if s, set := flag(args, "--workspace-status"); set {
			if !slices.Contains(b.columns, s) {
				return []byte(`{"ok":false,"error":{"code":"invalid_argument","message":"unknown status"}}`), nil
			}
			c.status = s
		}
		if s, set := flag(args, "--comment"); set {
			c.comment = s
		}
		if _, set := flag(args, "--unread"); set {
			c.unread = true
		}
		return ok(`{}`), nil
	}
	return nil, fmt.Errorf("unexpected orca %v", args)
}

func (b *board) links(context.Context) (map[unit.Unit]orca.Link, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[unit.Unit]orca.Link{}
	for u, id := range b.refs {
		out[u] = orca.Link{WorkspaceID: id, Review: true}
	}
	return out, nil
}

func (b *board) Place(u unit.Unit, column string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ref, _ := pr.ParseKey(u.Key)
	id := fmt.Sprintf("repo::/wt/%s-%s-%d", ref.Owner, ref.Repo, ref.Number)
	b.refs[u] = id
	b.cards[id] = &card{status: column}
}

func (b *board) Card(ref unit.Unit) (string, string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.cards[b.refs[ref]]
	return c.status, c.comment, c.unread
}

func (b *board) Column(s view.Status) string {
	return map[view.Status]string{view.Working: "reviewing", view.NeedsMe: "needs-me", view.Reviewed: "reviewed", view.Done: "completed", view.InProgress: "in-progress"}[s]
}

func (b *board) Unmanaged() string { return "parked" }

func (b *board) Typed() []turnstest.Typed {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.typed)
}

var allColumns = []string{"reviewing", "in-review", "needs-me", "reviewed", "completed", "in-progress", "ready-to-merge", "parked"}

func newAdapter(b *board) *orca.Adapter { return orca.New(&wsorca.Manager{R: b}, cols, b.links) }

func TestSessionsContract(t *testing.T) {
	turnstest.RunSessions(t, func(*testing.T) (turns.Sessions, func() []turnstest.Typed) {
		b := newBoard(allColumns...)
		return turnstest.WorldRouter(newAdapter(b)), b.Typed
	})
}

func TestCloseClosesTheTerminalThroughOrca(t *testing.T) {
	b := newBoard(allColumns...)
	if err := newAdapter(b).Close(context.Background(), "term_1"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(b.closed, []string{"term_1"}) {
		t.Fatalf("closed %v", b.closed)
	}
}

type staleRunner struct{ board }

func (r *staleRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	if args[0] == "terminal" && args[1] == "close" {
		return []byte(`{"ok":false,"error":{"code":"terminal_handle_stale","message":"gone"}}`), nil
	}
	return r.board.Run(ctx, args...)
}

func TestCloseOfAGoneHandleIsNotAnError(t *testing.T) {
	a := orca.New(&wsorca.Manager{R: &staleRunner{}}, cols, nil)
	if err := a.Close(context.Background(), "term_gone"); err != nil {
		t.Fatal(err)
	}
}

type listRunner struct{ list string }

func (r listRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	if args[0] == "status" {
		return []byte(`{"ok":true,"result":{"graphStatus":"ready"}}`), nil
	}
	return []byte(r.list), nil
}

func TestCensusCountsEveryRowAndKnowsACutShortList(t *testing.T) {
	ctx := context.Background()
	rows := `{"handle":"term_1","worktreeId":"ws","connected":false,"writable":false,"orphaned":true}`
	for _, c := range []struct {
		name, list string
		complete   bool
	}{
		{"an orphaned disconnected terminal is listed", `{"ok":true,"result":{"terminals":[` + rows + `],"truncated":false,"hostScope":{"hostIds":["local"],"omittedHostIds":[]}}}`, true},
		{"a list that does not report its coverage", `{"ok":true,"result":{"terminals":[` + rows + `]}}`, false},
		{"a truncated list", `{"ok":true,"result":{"terminals":[` + rows + `],"truncated":true,"hostScope":{"hostIds":["local"],"omittedHostIds":[]}}}`, false},
		{"an omitted host", `{"ok":true,"result":{"terminals":[` + rows + `],"truncated":false,"hostScope":{"omittedHostIds":["h1"]}}}`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := orca.New(&wsorca.Manager{R: listRunner{c.list}}, cols, nil)
			got, err := a.Census(ctx)
			if err != nil || got.Complete != c.complete || got.Handles["term_1"] != "ws" || len(got.Handles) != 1 {
				t.Fatalf("Census = %+v, %v", got, err)
			}
		})
	}
}

func TestATruncatedListStillServesTerminals(t *testing.T) {
	list := `{"ok":true,"result":{"terminals":[{"handle":"term_1","worktreeId":"ws","tabId":"tab-1","connected":true,"writable":true}],"truncated":true}}`
	a := orca.New(&wsorca.Manager{R: listRunner{list}}, cols, nil)
	got, err := a.Processes(context.Background(), "ws")
	if err != nil || len(got) != 1 || got[0].Handle != "term_1" {
		t.Fatalf("Processes = %+v, %v", got, err)
	}
}

func TestSinkContract(t *testing.T) {
	viewstest.RunSink(t, func(*testing.T) (views.ViewSink, views.PlacementReader, viewstest.Board) {
		b := newBoard(allColumns...)
		a := newAdapter(b)
		return a, a, b
	})
}

var r7 = pr.Ref{Owner: "acme", Repo: "api", Number: 7}.Unit()

type cannedPs string

func (c cannedPs) Run(context.Context, ...string) ([]byte, error) { return []byte(c), nil }

func TestAgentsMapRawStates(t *testing.T) {
	ps := cannedPs(`{"ok":true,"result":{"worktrees":[{"worktreeId":"w","agents":[
		{"paneKey":"tab-1:leaf-1","state":"done"},{"paneKey":"tab-2:leaf-1","state":"idle"}]}]}}`)
	got, err := orca.New(&wsorca.Manager{R: ps}, cols, nil).Agents(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("agents %+v, %v", got, err)
	}
	if got[0].State != turn.AgentDone || got[1].State.String() != "idle" {
		t.Fatalf("states %v %v", got[0].State, got[1].State)
	}
}

func TestPortsRefusesAnotherManager(t *testing.T) {
	if _, err := orca.Ports(&fake.Manager{ManagerName: "zed"}, cols, nil); err == nil || !strings.Contains(err.Error(), `"zed"`) {
		t.Fatalf("err = %v", err)
	}
	if a, err := orca.Ports(&wsorca.Manager{R: newBoard()}, cols, nil); err != nil || a == nil {
		t.Fatalf("orca: %v %v", a, err)
	}
}

func TestAgentsCarryTheirTerminalKey(t *testing.T) {
	ps := cannedPs(`{"ok":true,"result":{"worktrees":[{"worktreeId":"w","agents":[
		{"paneKey":"tab-1:leaf-2","state":"done"},{"paneKey":"odd","state":"done"}]}]}}`)
	agents, err := orca.New(&wsorca.Manager{R: ps}, cols, nil).Agents(context.Background())
	if err != nil || len(agents) != 2 || agents[0].TerminalKey != "tab-1" || agents[1].TerminalKey != "" {
		t.Fatalf("agents %+v, %v", agents, err)
	}
	terms, err := newAdapter(newBoard()).Processes(context.Background(), turnstest.World.WorkspaceID)
	if err != nil || len(terms) != 2 || terms[0].Key != "tab-1" || terms[1].Key != "tab-2" {
		t.Fatalf("terminals %+v, %v", terms, err)
	}
}

func TestWorkingFallsBackWhenTheBoardHasNoWorkingColumn(t *testing.T) {
	b := newBoard("in-review", "needs-me", "reviewed", "completed")
	b.Place(r7, "")
	a := newAdapter(b)
	got, err := a.Apply(context.Background(), []view.Item{{Unit: r7, Role: view.ReviewRole, Status: view.Working, Summary: "reviewing"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c, m, _ := b.Card(r7); c != "in-review" || m != "reviewing" {
		t.Fatalf("card = %q %q", c, m)
	}
	if len(got) != 1 || got[0].Role != view.ReviewRole || got[0].Column != "in-review" {
		t.Fatalf("applied = %+v, want the fallback column written", got)
	}
	if !a.Manages(view.ReviewRole, "in-review") {
		t.Fatal("the fallback column is not managed")
	}
}

func TestAuthorCardsManageOnlyNeedsMeAndInProgressWithoutFacts(t *testing.T) {
	a := newAdapter(newBoard())
	for column, want := range map[string]bool{"needs-me": true, "in-progress": true, "in-review": false, "ready-to-merge": false, "reviewed": false, "reviewing": false, "completed": false, "": false} {
		if got := a.Manages(view.AuthorRole, column); got != want {
			t.Errorf("Manages(author, %q) = %v", column, got)
		}
	}
}

func TestAuthorCardsManageTheAuthorColumnsWithFacts(t *testing.T) {
	withFacts := cols
	withFacts.AuthorColumns = true
	a := orca.New(&wsorca.Manager{R: newBoard()}, withFacts, newBoard().links)
	for column, want := range map[string]bool{"needs-me": true, "in-progress": true, "in-review": true, "ready-to-merge": true, "completed": true, "reviewed": false, "reviewing": false, "": false} {
		if got := a.Manages(view.AuthorRole, column); got != want {
			t.Errorf("Manages(author, %q) = %v", column, got)
		}
	}
}

func TestApplyWritesTheAuthorColumns(t *testing.T) {
	for status, want := range map[view.Status]string{view.Ready: "ready-to-merge", view.InProgress: "in-progress", view.Completed: "completed", view.InReview: "in-review"} {
		b := newBoard(allColumns...)
		b.Place(r7, "in-review")
		authorLinks := func(ctx context.Context) (map[unit.Unit]orca.Link, error) {
			out, err := b.links(ctx)
			for ref, l := range out {
				l.Review = false
				out[ref] = l
			}
			return out, err
		}
		withFacts := cols
		withFacts.AuthorColumns = true
		a := orca.New(&wsorca.Manager{R: b}, withFacts, authorLinks)
		got, err := a.Apply(context.Background(), []view.Item{{Unit: r7, Role: view.AuthorRole, Status: status, Summary: "why"}}, nil)
		if err != nil || len(got) != 1 || got[0].Column != want {
			t.Fatalf("%s: applied = %+v, %v, want column %s", status, got, err, want)
		}
		if c, _, _ := b.Card(r7); c != want {
			t.Errorf("%s: card = %q", status, c)
		}
		if got := a.Status(view.AuthorRole, want); got != status {
			t.Errorf("Status(author, %q) = %q, want %q", want, got, status)
		}
	}
}

func TestApplyReadsTheColumnFromTheBoard(t *testing.T) {
	b := newBoard(allColumns...)
	b.Place(r7, "reviewing")
	a := newAdapter(b)
	b.Place(r7, "parked")
	got, err := a.Apply(context.Background(), []view.Item{{Unit: r7, Role: view.ReviewRole, Status: view.NeedsMe, Unread: true, Summary: "asks: x?"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c, m, _ := b.Card(r7); c != "parked" || m != "asks: x?" {
		t.Fatalf("card = %q %q", c, m)
	}
	if len(got) != 1 || got[0].Override != "parked" || got[0].Column != "" {
		t.Fatalf("applied = %+v", got)
	}
}

func TestApplyReportsAWorkspaceTheBoardNoLongerLists(t *testing.T) {
	b := newBoard(allColumns...)
	b.Place(r7, "reviewing")
	b.mu.Lock()
	delete(b.cards, b.refs[r7])
	b.mu.Unlock()
	got, err := newAdapter(b).Apply(context.Background(), []view.Item{{Unit: r7, Role: view.ReviewRole, Status: view.NeedsMe}}, nil)
	if err == nil || len(got) != 0 {
		t.Fatalf("applied = %+v, %v", got, err)
	}
}

func TestApplyRefusesAStatusWithoutAColumn(t *testing.T) {
	b := newBoard(allColumns...)
	b.Place(r7, "")
	a := orca.New(&wsorca.Manager{R: b}, orca.Columns{NeedsMeStatus: "needs-me"}, b.links)
	got, err := a.Apply(context.Background(), []view.Item{{Unit: r7, Role: view.ReviewRole, Status: view.Working, Summary: "reviewing"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "working") || len(got) != 0 {
		t.Fatalf("applied = %+v, %v", got, err)
	}
	if c, m, _ := b.Card(r7); c != "" || m != "" {
		t.Fatalf("card = %q %q", c, m)
	}
	a = orca.New(&wsorca.Manager{R: b}, orca.Columns{ReviewFallbackStatus: "in-review"}, b.links)
	if _, err := a.Apply(context.Background(), []view.Item{{Unit: r7, Role: view.ReviewRole, Status: view.Working}}, nil); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := b.Card(r7); c != "in-review" {
		t.Fatalf("without a review column, working went to %q", c)
	}
}

func TestApplySkipsItemsWithoutTheirWorkspace(t *testing.T) {
	b := newBoard(allColumns...)
	b.Place(r7, "reviewing")
	a := newAdapter(b)
	r8 := pr.Ref{Owner: "acme", Repo: "api", Number: 8}.Unit()
	got, err := a.Apply(context.Background(), []view.Item{
		{Unit: r8, Role: view.ReviewRole, Status: view.NeedsMe},
		{Unit: r7, Role: view.AuthorRole, Status: view.NeedsMe},
	}, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("applied = %+v, %v", got, err)
	}
	if c, _, _ := b.Card(r7); c != "reviewing" {
		t.Fatalf("an author item moved the review card to %q", c)
	}
}

func TestApplyKeepsGoingPastAFailedCard(t *testing.T) {
	b := newBoard("needs-me")
	r8 := pr.Ref{Owner: "acme", Repo: "api", Number: 8}.Unit()
	b.Place(r7, "")
	b.Place(r8, "")
	got, err := newAdapter(b).Apply(context.Background(), []view.Item{
		{Unit: r7, Role: view.ReviewRole, Status: view.Reviewed, Summary: "commented"},
		{Unit: r8, Role: view.ReviewRole, Status: view.NeedsMe, Unread: true, Summary: "asks: x?"},
	}, nil)
	var ie *views.ItemError
	if !errors.As(err, &ie) || ie.Key != (view.Key{Unit: r7, Role: view.ReviewRole}) || !strings.Contains(err.Error(), "reviewed") {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 1 || got[0].Unit != r8 || got[0].Column != "needs-me" {
		t.Fatalf("applied = %+v", got)
	}
}

func TestTheAdapterFollowsTheRemappedColumns(t *testing.T) {
	remapped := cols
	remapped.InProgressStatus, remapped.CompletedStatus = "doing", "done"
	b := newBoard("reviewing", "needs-me", "doing", "done", "in-progress", "completed")
	b.Place(r7, "doing")
	a := orca.New(&wsorca.Manager{R: b}, remapped, b.links)
	for column, want := range map[string]bool{"doing": true, "in-progress": false} {
		if got := a.Manages(view.AuthorRole, column); got != want {
			t.Errorf("Manages(author, %q) = %v", column, got)
		}
	}
	for column, want := range map[string]bool{"done": true, "completed": false} {
		if got := a.Manages(view.ReviewRole, column); got != want {
			t.Errorf("Manages(review, %q) = %v", column, got)
		}
	}
	if _, err := a.Apply(context.Background(), []view.Item{{Unit: r7, Role: view.AuthorRole, Status: view.InProgress}}, nil); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := b.Card(r7); c != "doing" {
		t.Fatalf("card = %q", c)
	}
}

func (b *board) share(u, with unit.Unit) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refs[u] = b.refs[with]
}

func TestAUnitOfAnotherProviderIsSkipped(t *testing.T) {
	b := newBoard(allColumns...)
	b.Place(r7, "")
	thread := unit.Unit{Provider: "thread", Key: "C1 thread 9"}
	got, err := newAdapter(b).Apply(context.Background(), []view.Item{{Unit: thread, Role: view.ReviewRole, Status: view.Working}}, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("applied %+v, %v", got, err)
	}
	if c, _, _ := b.Card(r7); c != "" {
		t.Fatalf("card = %q", c)
	}
}

func TestTheNewerUnitKeepsASharedCardAcrossCalls(t *testing.T) {
	ctx := context.Background()
	b := newBoard(allColumns...)
	b.Place(r7, "")
	r8 := pr.Ref{Owner: "acme", Repo: "api", Number: 8}.Unit()
	b.share(r8, r7)
	a := newAdapter(b)
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

	if _, err := a.Apply(ctx, []view.Item{{Unit: r8, Role: view.ReviewRole, Status: view.NeedsMe, At: at.Add(time.Minute)}}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := a.Apply(ctx, []view.Item{{Unit: r7, Role: view.ReviewRole, Status: view.Working, At: at}}, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("the older unit applied %+v, %v", got, err)
	}
	if c, _, _ := b.Card(r7); c != "needs-me" {
		t.Fatalf("card = %q, want the newer unit's column", c)
	}
}

func (b *board) count(prefix string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, c := range b.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func cachedAdapter(t *testing.T, b *board) *orca.Adapter {
	t.Helper()
	m := &wsorca.Manager{R: b}
	a, err := orca.Ports(workspace.NewCached(m, m, 2*time.Minute), cols, b.links)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestApplySkipsUnchangedCard(t *testing.T) {
	b := newBoard(allColumns...)
	b.Place(r7, "needs-me")
	b.cards[b.refs[r7]].comment = "asks: x?"
	b.cards[b.refs[r7]].unread = true
	a := cachedAdapter(t, b)
	got, err := a.Apply(context.Background(), []view.Item{{Unit: r7, Role: view.ReviewRole, Status: view.NeedsMe, Unread: true, Summary: "asks: x?"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := b.count("worktree set"); n != 0 {
		t.Errorf("%d worktree set calls for a card that already matches", n)
	}
	if len(got) != 1 || got[0].Status != view.NeedsMe || got[0].Column != "needs-me" {
		t.Fatalf("applied = %+v", got)
	}
}

func TestApplyWritesOnDrift(t *testing.T) {
	for name, drift := range map[string]func(c *card){
		"status":  func(c *card) { c.status = "reviewing" },
		"comment": func(c *card) { c.comment = "old" },
		"unread":  func(c *card) { c.unread = false },
	} {
		t.Run(name, func(t *testing.T) {
			b := newBoard(allColumns...)
			b.Place(r7, "needs-me")
			c := b.cards[b.refs[r7]]
			c.comment, c.unread = "asks: x?", true
			drift(c)
			a := cachedAdapter(t, b)
			if _, err := a.Apply(context.Background(), []view.Item{{Unit: r7, Role: view.ReviewRole, Status: view.NeedsMe, Unread: true, Summary: "asks: x?"}}, nil); err != nil {
				t.Fatal(err)
			}
			if n := b.count("worktree set"); n != 1 {
				t.Errorf("%d worktree set calls, want 1", n)
			}
			if st, m, u := b.Card(r7); st != "needs-me" || m != "asks: x?" || !u {
				t.Errorf("card = %q %q %v", st, m, u)
			}
		})
	}
}

func TestApplyDecidesAWriteOnAFreshBoard(t *testing.T) {
	b := newBoard(allColumns...)
	b.Place(r7, "needs-me")
	a := cachedAdapter(t, b)
	if _, err := a.Placements(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.cards[b.refs[r7]].status = "parked"
	b.mu.Unlock()
	got, err := a.Apply(context.Background(), []view.Item{{Unit: r7, Role: view.ReviewRole, Status: view.NeedsMe, Summary: "asks"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st, _, _ := b.Card(r7); st != "parked" || len(got) != 1 || got[0].Override != "parked" {
		t.Fatalf("card = %q, applied = %+v: the stale column was overwritten", st, got)
	}
}

func TestPlacementsNoExtraList(t *testing.T) {
	b := newBoard(allColumns...)
	b.Place(r7, "needs-me")
	b.cards[b.refs[r7]].comment = "asks"
	a := cachedAdapter(t, b)
	ctx := context.Background()
	if _, err := a.Placements(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(ctx, []view.Item{{Unit: r7, Role: view.ReviewRole, Status: view.NeedsMe, Summary: "asks"}}, nil); err != nil {
		t.Fatal(err)
	}
	if n := b.count("worktree list"); n != 1 {
		t.Errorf("%d worktree list calls for a reconcile and a flush, want 1", n)
	}
}

func TestApplyForcedReadsFresh(t *testing.T) {
	b := newBoard(allColumns...)
	b.Place(r7, "reviewing")
	a := cachedAdapter(t, b)
	ctx := context.Background()
	if _, err := a.Placements(ctx); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.cards[b.refs[r7]].status = "parked"
	b.mu.Unlock()
	item := view.Item{Unit: r7, Role: view.ReviewRole, Status: view.Working}
	if _, err := a.Apply(ctx, []view.Item{item}, map[unit.Unit]bool{r7: true}); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := b.Card(r7); st != "reviewing" {
		t.Fatalf("card = %q, want the forced item to move it back to reviewing", st)
	}
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

type censusRunner struct{ list, status string }

func (r censusRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	switch {
	case args[0] == "status":
		return []byte(r.status), nil
	case r.list == "":
		return nil, errors.New("orca is down")
	}
	return []byte(r.list), nil
}

func TestAnIncompleteCensusFindsNothingGone(t *testing.T) {
	ready := `{"ok":true,"result":{"graphStatus":"ready"}}`
	listed := func(scope string) string {
		return `{"ok":true,"result":{"terminals":[{"handle":"term_1","worktreeId":"ws"}],` + scope + `},"_meta":{"runtimeId":"rt"}}`
	}
	whole := listed(`"truncated":false,"hostScope":{"hostIds":["local"],"omittedHostIds":[]}`)
	for _, c := range []struct {
		name   string
		runner censusRunner
		closes bool
	}{
		{"a complete census", censusRunner{whole, ready}, true},
		{"a truncated list", censusRunner{listed(`"truncated":true,"hostScope":{"hostIds":["local"],"omittedHostIds":[]}`), ready}, false},
		{"omitted hosts", censusRunner{listed(`"truncated":false,"hostScope":{"hostIds":["local"],"omittedHostIds":["ssh:box"]}`), ready}, false},
		{"a failed status.get", censusRunner{whole, `{"ok":false,"error":{"code":"runtime_unavailable","message":"gone"}}`}, false},
		{"a graph not ready", censusRunner{whole, `{"ok":true,"result":{"graphStatus":"loading"}}`}, false},
		{"a failed terminal list", censusRunner{"", ready}, false},
		{"a list with no runtime id", censusRunner{strings.TrimSuffix(whole, `,"_meta":{"runtimeId":"rt"}}`) + "}", ready}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			a := orca.New(&wsorca.Manager{R: c.runner}, cols, nil)
			reg := turnstest.NewRegistry()
			reg.Put(turn.Session{ID: "term_1", Manager: "orca", Handle: "term_1", WorkspaceID: "ws"})
			reg.Put(turn.Session{ID: "term_gone", Manager: "orca", Handle: "term_gone", WorkspaceID: "ws"})
			s := &turns.Service{Store: turnstest.NewStore(), Registry: reg, Sessions: turns.NewRouter(reg, a), Clock: wallClock{}, FastJob: time.Nanosecond}
			var gone []string
			for range 3 {
				a.Refresh()
				_ = s.Census(ctx)
			}
			for _, h := range []string{"term_1", "term_gone"} {
				if s.ProcessGone(h) {
					gone = append(gone, h)
				}
			}
			var want []string
			if c.closes {
				want = []string{"term_gone"}
			}
			if !slices.Equal(gone, want) {
				t.Fatalf("gone %v, want %v", gone, want)
			}
		})
	}
}

type countingPs struct {
	cannedPs
	n *int
}

func (c countingPs) Run(ctx context.Context, args ...string) ([]byte, error) {
	*c.n++
	return c.cannedPs.Run(ctx, args...)
}

func TestLastAgentsReadsTheSnapshotOnly(t *testing.T) {
	calls := 0
	ps := countingPs{cannedPs(`{"ok":true,"result":{"worktrees":[{"worktreeId":"w","agents":[{"paneKey":"tab-1:leaf-1","state":"waiting"}]}]}}`), &calls}
	a := orca.New(&wsorca.Manager{R: ps}, cols, nil)
	if agents, ok := a.LastAgents(); ok || agents != nil || calls != 0 {
		t.Fatalf("before any read: %+v %v, %d calls", agents, ok, calls)
	}
	if _, err := a.Agents(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := calls
	agents, ok := a.LastAgents()
	if !ok || len(agents) != 1 || agents[0].State != turn.AgentWaiting || agents[0].TerminalKey != "tab-1" || calls != before {
		t.Fatalf("after a read: %+v %v, %d calls after %d", agents, ok, calls, before)
	}
}
