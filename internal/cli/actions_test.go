package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/daemon"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

type recordingActions struct {
	mu       sync.Mutex
	calls    []string
	profiles []string
	// store, when set, is marked by an adopt as the real take-over does.
	store *inventory.Store
}

func (f *recordingActions) add(s string, c daemon.Call) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
	f.profiles = append(f.profiles, c.AgentHome)
	fmt.Fprintf(c.Out, "did %s\n", s)
	return nil
}

func (f *recordingActions) Open(_ context.Context, u unit.Unit, c daemon.Call) error {
	r := prOf(u)
	return f.add("open "+r.String(), c)
}
func (f *recordingActions) Adopt(ctx context.Context, u unit.Unit, c daemon.Call) error {
	r := prOf(u)
	if f.store != nil {
		if err := f.store.SetAdopted(ctx, r); err != nil {
			return err
		}
	}
	return f.add("adopt "+r.String(), c)
}
func (f *recordingActions) Intent(_ context.Context, name string, u unit.Unit, c daemon.Call) error {
	r := prOf(u)
	return f.add(name+" "+r.String()+" "+c.Guidance, c)
}
func (f *recordingActions) Run(_ context.Context, u unit.Unit, c daemon.Call) error {
	r := prOf(u)
	return f.add("run "+r.String(), c)
}
func (f *recordingActions) TrustAndRun(_ context.Context, u unit.Unit, c daemon.Call) error {
	r := prOf(u)
	return f.add("trust_run "+r.String(), c)
}
func (f *recordingActions) Unblock(_ context.Context, u unit.Unit, c daemon.Call) error {
	r := prOf(u)
	return f.add("unblock "+r.String(), c)
}

func (f *recordingActions) Answer(_ context.Context, u unit.Unit, c daemon.Call) error {
	r := prOf(u)
	return f.add("answer "+r.String()+" "+c.Guidance, c)
}

func (f *recordingActions) Remove(_ context.Context, u unit.Unit, c daemon.Call) error {
	r := prOf(u)
	return f.add("remove "+r.String(), c)
}

func TestPauseRequiresDaemon(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.noDaemon(t)
	if code := te.run("pause", r7.URL()); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(te.err.String(), brand.Command+" serve") {
		t.Fatalf("stderr: %s", te.err)
	}
}

func TestMuteParsesUntil(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, nil)
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = d.socket
	if code := te.run("mute", r7.URL(), "--until", "36h"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	o, _ := d.store.Override(context.Background(), r7)
	if !o.MutedUntil.Equal(daemonNow.Add(36 * time.Hour)) {
		t.Fatalf("muted until %v", o.MutedUntil)
	}
	if code := te.run("unmute", r7.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if code := te.run("mute", r7.URL(), "--until-state"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	o, _ = d.store.Override(context.Background(), r7)
	if !o.MuteUntilStateChange || !o.MutedUntil.IsZero() {
		t.Fatalf("override %+v", o)
	}
	if code := te.run("mute", r7.URL()); code == 0 {
		t.Fatal("mute without a duration succeeded")
	}
}

func TestReviewRoutesToDaemonWhenUp(t *testing.T) {
	t.Parallel()
	fa := &recordingActions{}
	d := startDaemon(t, fa)
	m := &fake.Manager{}
	te := newTestEnv(t, m)
	te.Socket = d.socket
	te.LoadConfig = nil
	d.holdByCap(t)
	if code := te.run("review", r7.URL(), "look at", "the migration"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !slices.Equal(fa.calls, []string{"review acme/api#7 look at the migration"}) {
		t.Fatalf("calls %v", fa.calls)
	}
	if len(m.Writes) != 0 || len(m.Prompts) != 0 || len(te.gh.calls) != 0 {
		t.Fatalf("an inline round ran: writes %v prompts %v gh %v", m.Writes, m.Prompts, te.gh.calls)
	}
	for _, args := range [][]string{{"take-over", r7.URL()}, {"address", r7.URL(), "skip the nit"}, {"open", r7.URL()}} {
		if code := te.run(args...); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, te.err)
		}
	}
	want := []string{"review acme/api#7 look at the migration", "adopt acme/api#7", "address acme/api#7 skip the nit", "open acme/api#7"}
	if !slices.Equal(fa.calls, want) {
		t.Fatalf("calls %v", fa.calls)
	}
}

func TestOpenWithoutURLOpensTheCockpit(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, nil)
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = d.socket
	if err := os.WriteFile(filepath.Join(te.StateDir, "http.json"), []byte(`{"port":4321,"token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var opened []string
	te.Sh = func(_ context.Context, name string, args ...string) ([]byte, error) {
		opened = append(opened, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	if code := te.run("open"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if want := browserCommand() + " http://127.0.0.1:4321/?token=tok"; !slices.Equal(opened, []string{want}) {
		t.Fatalf("opened %v", opened)
	}
}

func cockpitEnv(t *testing.T) (*testEnv, *[]string) {
	t.Helper()
	d := startDaemon(t, nil)
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = d.socket
	if err := os.WriteFile(filepath.Join(te.StateDir, "http.json"), []byte(`{"port":4321,"token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var opened []string
	te.Sh = func(_ context.Context, name string, args ...string) ([]byte, error) {
		opened = append(opened, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	return te, &opened
}

func TestOpenPrintShowsTheCockpitURL(t *testing.T) {
	t.Parallel()
	r7 := pr.Ref{Owner: "acme", Repo: "api", Number: 7}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"cockpit", []string{"open", "--print"}, "http://127.0.0.1:4321/?token=tok\n"},
		{"view", []string{"open", "--print", "--view", "stalled"}, "http://127.0.0.1:4321/?token=tok#view=stalled\n"},
		{"recent", []string{"open", "--print", "--view", "recent"}, "http://127.0.0.1:4321/?token=tok#view=recent\n"},
		{"pull request", []string{"open", r7.URL(), "--print"}, "http://127.0.0.1:4321/?token=tok#unit=github/acme%2Fapi%237\n"},
		{"view and pull request", []string{"open", r7.URL(), "--view", "mine", "--print"}, "http://127.0.0.1:4321/?token=tok#view=mine&unit=github/acme%2Fapi%237\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			te, opened := cockpitEnv(t)
			if code := te.run(c.args...); code != 0 {
				t.Fatalf("exit %d: %s", code, te.err)
			}
			if te.out.String() != c.want {
				t.Fatalf("stdout %q, want %q", te.out, c.want)
			}
			if len(*opened) != 0 {
				t.Fatalf("opened %v", *opened)
			}
			if strings.Contains(te.err.String(), "tok") {
				t.Fatalf("stderr leaks the token: %s", te.err)
			}
		})
	}
}

func TestCockpitURLTakesARoute(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := cockpitURL(dir, ""); err == nil || !strings.Contains(err.Error(), "cockpit address") {
		t.Fatalf("no address file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, httpInfoFile), []byte(`{"port":4321,"token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r7 := pr.Ref{Owner: "acme", Repo: "api", Number: 7}
	cases := []struct{ route, want string }{
		{"", "http://127.0.0.1:4321/?token=tok"},
		{"view=loop", "http://127.0.0.1:4321/?token=tok#view=loop"},
		{"task=e-1", "http://127.0.0.1:4321/?token=tok#task=e-1"},
		{"task=draft:7", "http://127.0.0.1:4321/?token=tok#task=draft:7"},
		{unitRoute(r7.Unit()), "http://127.0.0.1:4321/?token=tok#unit=github/acme%2Fapi%237"},
		{"view=needs-me&" + unitRoute(r7.Unit()), "http://127.0.0.1:4321/?token=tok#view=needs-me&unit=github/acme%2Fapi%237"},
	}
	for _, c := range cases {
		got, err := cockpitURL(dir, c.route)
		if err != nil || got != c.want {
			t.Errorf("cockpitURL(%q) = %q, %v; want %q", c.route, got, err, c.want)
		}
	}
}

func TestOpenViewOpensTheCockpitOnThatView(t *testing.T) {
	t.Parallel()
	te, opened := cockpitEnv(t)
	if code := te.run("open", "--view", "mine"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if want := browserCommand() + " http://127.0.0.1:4321/?token=tok#view=mine"; !slices.Equal(*opened, []string{want}) {
		t.Fatalf("opened %v", *opened)
	}
}

func TestOpenRejectsAnUnknownView(t *testing.T) {
	t.Parallel()
	te, opened := cockpitEnv(t)
	if code := te.run("open", "--print", "--view", "later"); code == 0 {
		t.Fatalf("accepted an unknown view: %s", te.out)
	}
	if !strings.Contains(te.err.String(), "later") {
		t.Fatalf("stderr: %s", te.err)
	}
	if te.out.Len() != 0 || len(*opened) != 0 {
		t.Fatalf("stdout %q, opened %v", te.out, *opened)
	}
}

func TestOpenPrintNeedsTheDaemon(t *testing.T) {
	t.Parallel()
	te, _ := cockpitEnv(t)
	te.Socket = filepath.Join(t.TempDir(), "none.sock")
	if code := te.run("open", "--print"); code == 0 {
		t.Fatalf("printed without a daemon: %s", te.out)
	}
	if te.out.Len() != 0 {
		t.Fatalf("stdout %q", te.out)
	}
}

type unfocusable struct{ *fake.Manager }

func (unfocusable) Focus(context.Context, string) error {
	return errors.New("no terminal and no README")
}

func openStore(t *testing.T) *inventory.Store {
	t.Helper()
	stDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	st := inventory.New(stDB)
	t.Cleanup(func() { stDB.Close() })
	return st
}

func TestOpenFallsBackToTheBrowser(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		ws      string
		focusOK bool
		want    []string
	}{
		"workspace focused":     {ws: "ws-1", focusOK: true},
		"focus fails":           {ws: "ws-1", want: []string{browserCommand() + " " + r7.URL()}},
		"no workspace attached": {focusOK: true, want: []string{browserCommand() + " " + r7.URL()}},
	} {
		t.Run(name, func(t *testing.T) {
			m := &fake.Manager{}
			te := newTestEnv(t, m)
			if !c.focusOK {
				te.WS = workspace.NewSet(unfocusable{m})
			}
			var opened []string
			te.Sh = func(_ context.Context, name string, args ...string) ([]byte, error) {
				opened = append(opened, name+" "+strings.Join(args, " "))
				return nil, nil
			}
			st := openStore(t)
			ctx := context.Background()
			if err := st.UpsertPR(ctx, dinv.PR{Ref: r7, State: "OPEN", URL: r7.URL()}); err != nil {
				t.Fatal(err)
			}
			if c.ws != "" {
				if err := st.SetWorkspace(ctx, r7, dinv.Workspace{ID: c.ws}); err != nil {
					t.Fatal(err)
				}
			}
			if err := (envActions{te.Env, st}).Open(ctx, r7.Unit(), daemon.Call{Out: io.Discard}); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(opened, c.want) {
				t.Fatalf("opened %v", opened)
			}
			if focused := slices.Contains(m.Writes, "focus ws-1"); focused != (c.ws != "" && c.focusOK) {
				t.Fatalf("writes %v", m.Writes)
			}
		})
	}
}

func TestOpenFocusesAMainCheckout(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{WorkspaceList: []wsdomain.Workspace{{ID: "main-api", Path: "/src/api", IsMain: true, HostID: "local", Branch: "feat/x"}}}
	te := newTestEnv(t, m)
	te.Sh = func(_ context.Context, name string, args ...string) ([]byte, error) {
		t.Fatalf("opened %s %v", name, args)
		return nil, nil
	}
	st := openStore(t)
	ctx := context.Background()
	if err := st.UpsertPR(ctx, dinv.PR{Ref: r7, State: "OPEN", URL: r7.URL()}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetWorkspace(ctx, r7, dinv.Workspace{ID: "main-api", DisplayName: "api"}); err != nil {
		t.Fatal(err)
	}
	if err := (envActions{te.Env, st}).Open(ctx, r7.Unit(), daemon.Call{Out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Writes, []string{"focus main-api"}) {
		t.Fatalf("writes %v", m.Writes)
	}
}

func TestRoundsAskedByHandDoNotCountAgainstTheCap(t *testing.T) {
	t.Parallel()
	sc := addressScenario(t)
	sc.te.gh.basics = `{"data":{"pr":{"pullRequest":{"number":9,"title":"Add z","state":"OPEN","headRefName":"feat/z","headRefOid":"hfeat/z","repository":{"nameWithOwner":"acme/api"}}}}}`
	if err := (envActions{sc.env, nil}).Intent(context.Background(), "address", sc.ref.Unit(), daemon.Call{Guidance: "skip the nit", Out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	st, _ := sc.te.Rounds.Load(context.Background())
	if why := st.CapReason(sc.ref.String(), testNow, 1); why != "" {
		t.Fatalf("a triage asked by hand counted against the cap: %s", why)
	}
	if st.Mark(sc.ref.String(), ghsource.MarkAddressedReviews) == "" {
		t.Fatal("the triage was not recorded; the next sweep would send it again")
	}
}

func TestRunSendsTheHeldFollowupNow(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	exhaustCap(t, sc)
	sc.te.gh.basics = openBasics
	if err := (envActions{sc.env, nil}).Run(context.Background(), sc.ref.Unit(), daemon.Call{Out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if len(sc.ws.Prompts) != 1 {
		t.Fatalf("prompts %+v", sc.ws.Prompts)
	}
	if h, _, _, _ := promptOf(t, sc.ws.Prompts[0].Text); h != "Follow-up review of "+sc.ref.String()+", started by "+brand.Name+"." {
		t.Fatalf("prompts %+v", sc.ws.Prompts)
	}
	st, _ := sc.te.Rounds.Load(context.Background())
	if why := st.CapReason(sc.ref.String(), testNow, 2); why != "" {
		t.Fatalf("run counted against the cap: %s", why)
	}
}

func TestDaemonRoutedCommandsForwardTheCallersProfile(t *testing.T) {
	t.Parallel()
	fa := &recordingActions{}
	d := startDaemon(t, fa)
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = d.socket
	te.withCallerHome("/home/me/.claude-personal")
	d.holdByCap(t)
	for _, args := range [][]string{{"review", r7.URL()}, {"take-over", r7.URL()}, {"address", r7.URL()}} {
		if code := te.run(args...); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, te.err)
		}
	}
	want := slices.Repeat([]string{"/home/me/.claude-personal"}, 3)
	if !slices.Equal(fa.profiles, want) {
		t.Fatalf("profiles %q", fa.profiles)
	}
	if !strings.Contains(te.out.String(), "did adopt acme/api#7\n") {
		t.Fatalf("the action's output was not printed: %s", te.out)
	}
}

const personal = "/home/me/.claude-personal"

func TestDelegatedAdoptUsesTheCallersProfileElseTheRemembered(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		profile, remembered string
	}{
		"caller's profile":      {profile: personal, remembered: "/home/me/.claude-other"},
		"remembered for the PR": {remembered: personal},
	} {
		t.Run(name, func(t *testing.T) {
			m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
			te := newTestEnv(t, m)
			te.withCallerHome("/home/daemon/.claude-launchd")
			te.gh.basics = adoptBasics
			te.git.branch["/worktrees/feat/theirs"] = "feat/theirs"
			st, _ := te.Rounds.Load(context.Background())
			st.SetAgentHome("acme/api#20", c.remembered)
			if err := te.Rounds.Save(context.Background(), st); err != nil {
				t.Fatal(err)
			}
			ref := pr.Ref{Owner: "acme", Repo: "api", Number: 20}
			var out strings.Builder
			if err := (envActions{te.Env, nil}).Adopt(context.Background(), ref.Unit(), daemon.Call{AgentHome: c.profile, Out: &out}); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(launches(m.Commands), "|"); got != "CLAUDE_CONFIG_DIR='"+personal+"' claude" {
				t.Fatalf("agent commands %q", got)
			}
			if !strings.Contains(out.String(), "created feat/theirs") || !strings.Contains(te.out.String(), "created feat/theirs") {
				t.Fatalf("call output %q, daemon output %q", out.String(), te.out)
			}
		})
	}
}

func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", brand.Name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestReviewFailsWhenTheDaemonIsDown(t *testing.T) {
	t.Parallel()
	for name, socket := range map[string]func(t *testing.T) string{
		"no socket": func(t *testing.T) string { return filepath.Join(shortDir(t), "daemon.sock") },
		"stale socket": func(t *testing.T) string {
			path := filepath.Join(shortDir(t), "daemon.sock")
			l, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			l.(*net.UnixListener).SetUnlinkOnClose(false)
			l.Close()
			if _, err := os.Stat(path); err != nil {
				t.Fatal(err)
			}
			return path
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
			te := newTestEnv(t, m)
			te.Socket = socket(t)
			te.gh.basics = openBasics
			te.git.branch["/worktrees/REVIEW feat/x"] = "feat/x"
			if code := te.run("review", r7.URL()); code != 1 || !strings.Contains(te.err.String(), "launchctl kickstart") {
				t.Fatalf("exit %d: %s", code, te.err)
			}
			if len(m.Prompts) != 0 {
				t.Fatalf("a round went out without the daemon: %+v", m.Prompts)
			}
		})
	}
}

func TestReviewFailsWhenALiveDaemonDoesNotAnswer(t *testing.T) {
	t.Parallel()
	path := filepath.Join(shortDir(t), "daemon.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "database is locked", http.StatusInternalServerError)
	})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.Socket = path
	te.gh.basics = openBasics
	if code := te.run("review", r7.URL()); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(te.err.String(), "database is locked") {
		t.Fatalf("stderr: %s", te.err)
	}
	if len(m.Writes) != 0 || len(m.Prompts) != 0 {
		t.Fatalf("ran inline beside a live daemon: writes %v prompts %v", m.Writes, m.Prompts)
	}
}

// daemonForActions answers every request with 200 on a socket and records
// "METHOD path body".
func daemonForActions(t *testing.T) (*testEnv, func() []string) {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = filepath.Join(shortDir(t), "daemon.sock")
	var mu sync.Mutex
	var calls []string
	ln, err := net.Listen("unix", te.Socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		line := r.Method + " " + r.URL.Path
		if b := strings.TrimSpace(string(body)); b != "" {
			line += " " + b
		}
		mu.Lock()
		calls = append(calls, line)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return te, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
}

func TestPauseAndResumeRouteToTheDaemon(t *testing.T) {
	t.Parallel()
	te, calls := daemonForActions(t)
	if code := te.run("pause"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if code := te.run("resume"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if code := te.run("pause", "acme/sandbox", r7.URL(), "acme/web"); code != 0 ||
		!strings.Contains(te.out.String(), "paused; '"+brand.Command+" resume acme/sandbox "+r7.URL()+" acme/web' restarts them") {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	if code := te.run("resume", "acme/sandbox"); code != 0 || !strings.Contains(te.out.String(), "on acme/sandbox resumed") {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	if code := te.run("pause", "--except", "acme/web"); code != 1 || !strings.Contains(te.err.String(), "neither a pull request URL nor an owner/repo") {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	if got := calls(); !slices.Contains(got, `POST /api/pause {"paused":true}`) || !slices.Contains(got, `POST /api/pause {"paused":false}`) ||
		!slices.Contains(got, `POST /api/pause {"paused":true,"repos":["acme/sandbox","acme/web"],"prs":["acme/api#7"]}`) ||
		!slices.Contains(got, `POST /api/pause {"paused":false,"repos":["acme/sandbox"]}`) {
		t.Fatalf("calls %v", got)
	}
}

func TestNudgeRoutesToTheDaemon(t *testing.T) {
	t.Parallel()
	te, calls := daemonForActions(t)
	if code := te.run("nudge", r7.URL(), "be", "brief"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := calls(); !slices.ContainsFunc(got, func(c string) bool {
		return strings.HasPrefix(c, "POST /api/units/github/acme/api#7/nudge") && strings.Contains(c, `"guidance":"be brief"`)
	}) {
		t.Fatalf("calls %v", got)
	}
}

func TestNudgeNeedsTheDaemon(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.noDaemon(t)
	if code := te.run("nudge", r7.URL()); code != 1 || !strings.Contains(te.err.String(), brand.Command+" serve") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if code := te.run("nudge"); code != 1 || !strings.Contains(te.err.String(), "usage: "+brand.Command+" nudge") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func TestTrackAndUntrackGoThroughTheDaemon(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, nil)
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = d.socket
	for _, verb := range []string{"track", "untrack"} {
		if code := te.run(verb, r7.URL()); code != 0 {
			t.Fatalf("%s: %d %s", verb, code, te.err.String())
		}
	}
	if seen := d.seen(); !slices.Contains(seen, "POST /api/units/github/acme/api#7/track") || !slices.Contains(seen, "POST /api/units/github/acme/api#7/untrack") {
		t.Fatalf("requests %v", seen)
	}
}

func TestBriefRefreshGoesThroughTheDaemon(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, nil)
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = d.socket
	code := te.run("brief", "refresh", r7.URL())
	if !slices.Contains(d.seen(), "POST /api/units/github/acme/api#7/brief_refresh") {
		t.Fatalf("requests %v", d.seen())
	}
	if code == 0 || !strings.Contains(te.err.String(), "brief is off") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	te2 := newTestEnv(t, &fake.Manager{})
	te2.noDaemon(t)
	if code := te2.run("brief", "refresh", r7.URL()); code == 0 || !strings.Contains(te2.err.String(), brand.Command+" serve") {
		t.Fatalf("no daemon: exit %d: %s", code, te2.err)
	}
}
