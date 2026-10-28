package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/domain/pr"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
)

// errDaemonDown is every command's answer when no daemon listens: the loop's
// state is the daemon's alone.
var errDaemonDown = client.ErrDown

// daemonClient is the client of the daemon listening on the socket, or
// errDaemonDown. A daemon that accepts the connection but fails to answer is
// another error.
func (e *Env) daemonClient(ctx context.Context) (*client.Client, error) {
	if e.Socket == "" {
		return nil, errDaemonDown
	}
	c := e.agentClient()
	err := c.Ping(ctx)
	switch {
	case err == nil:
		return c, nil
	case errors.Is(err, client.ErrDown):
		return nil, errDaemonDown
	}
	return nil, fmt.Errorf("the daemon on %s did not answer: %w", e.Socket, err)
}

// action runs a pull request action on the daemon with the caller's agent
// home and prints its output, then done once it succeeds.
func (e *Env) action(ctx context.Context, ref pr.Ref, action string, req api.ActionRequest, done string) error {
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	return e.delegate(ctx, c, ref, action, req, done)
}

// delegate runs the action on the daemon with the caller's agent home
// and prints its output, then done once it succeeds.
func (e *Env) delegate(ctx context.Context, c *client.Client, ref pr.Ref, action string, req api.ActionRequest, done string) error {
	req.AgentHome = e.callerAgentHome(config.Config{Agent: e.callerKind()})
	req.Source = sourceCLI
	out, err := c.Action(ctx, ref.Unit(), action, req)
	printLines(e.Out, out)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "%s %s: %s\n", commandOf(action), ref, done)
	return nil
}

const sourceCLI = "cli"

// commandOf is the command that runs the catalog action id.
func commandOf(id string) string {
	if id == "take_over" {
		return "take-over"
	}
	return id
}

// prConfig is the config of ref's project, with profile as the agent home
// when the project sets none.
func prConfig(base config.Config, ref pr.Ref, profile string) config.Config {
	cfg := base.For(ref.Owner, ref.Repo)
	if cfg.AgentHome == "" {
		cfg.AgentHome = profile
	}
	return cfg
}

func printLines(w io.Writer, lines []string) {
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
}

func runPRAction(ctx context.Context, e *Env, name string, args []string) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(e.Err)
	var req api.ActionRequest
	usage := "usage: " + brand.Command + " " + name + " <https://github.com/<owner>/<repo>/pull/<n>>"
	if name == "mute" {
		fs.StringVar(&req.Until, "until", "", "mute for a duration such as 2d or 36h")
		fs.BoolVar(&req.UntilState, "until-state", false, "mute until who the pull request waits on changes")
		usage += " [--until 2d|--until-state]"
	}
	if name == "remove" {
		fs.BoolVar(&req.Force, "force", false, "remove whatever the workspace holds; only an open pull request still refuses")
		usage += " [--force]"
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New(usage)
	}
	ref, err := pr.ParseURL(pos[0])
	if err != nil {
		return err
	}
	return e.action(ctx, ref, name, req, "done")
}

// parseInterspersed accepts flags after the positional arguments, as in
// `mute <url> --until 2d`.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

var cockpitViews = []string{"needs-me", "stalled", "mine", "loop", "dash", "done", "all", "recent", "explore"}

func runOpen(ctx context.Context, e *Env, args []string) error {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	printURL := fs.Bool("print", false, "print the cockpit URL with its token instead of opening it")
	view := fs.String("view", "", "the cockpit view: "+strings.Join(cockpitViews, ", "))
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return errors.New("usage: " + brand.Command + " open [<https://github.com/<owner>/<repo>/pull/<n>>] [--print] [--view needs-me|stalled|mine|loop|dash|done|all|recent|explore]")
	}
	if *view != "" && !slices.Contains(cockpitViews, *view) {
		return fmt.Errorf("unknown view %q; one of %s", *view, strings.Join(cockpitViews, ", "))
	}
	var ref pr.Ref
	if len(pos) == 1 {
		if ref, err = pr.ParseURL(pos[0]); err != nil {
			return err
		}
		if !*printURL && *view != "" {
			return errors.New("--view with a pull request URL needs --print")
		}
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	if len(pos) == 1 && !*printURL {
		out, err := c.Action(ctx, ref.Unit(), "open", api.ActionRequest{})
		printLines(e.Out, out)
		return err
	}
	route := ""
	if *view != "" {
		route = "view=" + url.QueryEscape(*view)
	}
	if len(pos) == 1 {
		route = strings.TrimPrefix(route+"&"+unitRoute(ref.Unit()), "&")
	}
	u, err := cockpitURL(e.StateDir, route)
	if err != nil {
		return err
	}
	if *printURL {
		_, err := fmt.Fprintln(e.Out, u)
		return err
	}
	return e.openInBrowser(ctx, u)
}

// cockpitURL is the cockpit address with its token, at route, a hash
// route of the cockpit such as "view=inbox", "task=<id>" or unitRoute's.
func cockpitURL(stateDir, route string) (string, error) {
	info, err := daemon.ReadInfo(filepath.Join(stateDir, httpInfoFile))
	if err != nil {
		return "", fmt.Errorf("reading the cockpit address: %w", err)
	}
	u := "http://127.0.0.1:" + strconv.Itoa(info.Port) + "/?token=" + info.Token
	if route != "" {
		u += "#" + route
	}
	return u, nil
}

// unitRoute is the cockpit route of u's detail.
func unitRoute(u unit.Unit) string {
	return "unit=" + string(u.Provider) + "/" + url.QueryEscape(u.Key)
}

func browserCommand() string {
	if goos == "darwin" {
		return "open"
	}
	return "xdg-open"
}

func (e *Env) openInBrowser(ctx context.Context, url string) error {
	_, err := e.Sh(ctx, browserCommand(), url)
	return err
}

// envActions runs the daemon's delegated actions with the same code as the
// inline commands. The daemon's mutex keeps them from overlapping the sweep.
type envActions struct {
	e     *Env
	store *inventory.Store
}

var (
	_ daemon.Actions     = envActions{}
	_ daemon.AheadReader = envActions{}
	_ daemon.Unblocker   = envActions{}
)

func (a envActions) ReadAhead(ctx context.Context, u unit.Unit, full bool) context.Context {
	ref, err := githubRef(u)
	if err != nil {
		return ctx
	}
	return a.e.ReadAhead(ctx, ref, full)
}

// call is the Env an action runs with: what it reports goes to the daemon's
// log and back to the caller.
func (a envActions) call(c daemon.Call) *Env {
	e := *a.e
	if c.Out != nil {
		e.Out = io.MultiWriter(a.e.Out, c.Out)
		e.Err = io.MultiWriter(a.e.Err, c.Out)
	}
	return &e
}

// prepare loads the config and picks the agent home: the caller's,
// else the one remembered for the PR.
func (a envActions) prepare(ctx context.Context, ref pr.Ref, c daemon.Call) (*Env, config.Config, string, error) {
	cfg, err := a.e.loadConfig()
	if err != nil {
		return nil, cfg, "", err
	}
	if c.AgentHome != "" {
		return a.call(c), cfg, c.AgentHome, nil
	}
	rounds, err := a.e.rounds()
	if err != nil {
		return nil, cfg, "", err
	}
	st, err := rounds.Load(ctx)
	if err != nil {
		return nil, cfg, "", err
	}
	return a.call(c), cfg, st.AgentHome(ref.String()), nil
}

func (a envActions) Open(ctx context.Context, u unit.Unit, c daemon.Call) error {
	e := a.call(c)
	if !isPR(u) {
		return e.focusRunWorkspace(ctx, u)
	}
	ref := prOf(u)
	url := ref.URL()
	if a.store != nil {
		pr, ok, err := a.store.PR(ctx, ref)
		if err != nil {
			return err
		}
		if ok && pr.URL != "" {
			url = pr.URL
		}
		if ok && pr.Workspace.ID != "" {
			err := e.WS.Focus(ctx, wsdomain.Workspace{Manager: pr.Workspace.Manager, ID: pr.Workspace.ID})
			if err == nil {
				return nil
			}
			fmt.Fprintf(e.Err, "%s warning: %s: focusing the workspace: %v; opening the pull request instead\n", e.stamp(), ref, err)
		}
	}
	return e.openInBrowser(ctx, url)
}

func (a envActions) Adopt(ctx context.Context, u unit.Unit, c daemon.Call) error {
	ref := prOf(u)
	e, cfg, profile, err := a.prepare(ctx, ref, c)
	if err != nil {
		return err
	}
	if _, err = adoptPR(ctx, e, cfg, ref, profile); err != nil || a.store == nil {
		return err
	}
	return a.store.SetAdopted(ctx, ref)
}

func (a envActions) Intent(ctx context.Context, name string, u unit.Unit, c daemon.Call) error {
	if !slices.Contains(builtin.RoundProviders, u.Provider) {
		e := a.call(c)
		d, err := e.intentDef(name)
		if err != nil {
			return err
		}
		return e.liftPassedBlock(ctx, u, e.unitRound(ctx, d, u, c))
	}
	ref := prOf(u)
	e, cfg, profile, err := a.prepare(ctx, ref, c)
	if err != nil {
		return err
	}
	d, err := e.intentDef(name)
	if err != nil {
		return err
	}
	return e.liftPassedBlock(ctx, u, e.askedRound(ctx, d, cfg, ref, c.Guidance, c.Inputs, profile))
}

func (a envActions) Run(ctx context.Context, u unit.Unit, c daemon.Call) error {
	ref := prOf(u)
	e, cfg, profile, err := a.prepare(ctx, ref, c)
	if err != nil {
		return err
	}
	return e.liftPassedBlock(ctx, u, runHeldRound(ctx, e, cfg, ref, profile))
}

// liftPassedBlock records the start block of u lifted when err, the
// outcome of a round by hand, shows its session got past the question the
// block waits on: stopped on another question, or never initializing the
// loop's MCP server with none on screen. It returns err.
func (e *Env) liftPassedBlock(ctx context.Context, u unit.Unit, err error) error {
	var asked *turns.QuestionError
	if !errors.As(err, &asked) && !errors.As(err, new(errNoMCP)) {
		return err
	}
	b, berr := e.startBlockOf(ctx, u)
	if berr != nil || (asked != nil && asked.Question == b.Question) {
		return err
	}
	e.recordLifted(ctx, u)
	return err
}

// recordLifted records u's start block lifted by a later session.
func (e *Env) recordLifted(ctx context.Context, u unit.Unit) {
	ev := port.Event{Unit: u, Kind: dinv.KindRoundUnblocked, At: e.Now(), Data: "a later session got past its question"}
	if err := e.Rec.RecordUnit(ctx, ev); err != nil {
		fmt.Fprintf(e.Err, "warning: %s: recording the lifted block: %v\n", u.Label(), err)
	}
}

// runHeldRound sends at once the round a cap held back: a follow-up in the
// review workspace, else a round of the intent run.resolve picks for the
// pull request's role. It counts as asked for by the user, so it never
// counts against the caps.
func runHeldRound(ctx context.Context, e *Env, base config.Config, ref pr.Ref, profile string) error {
	cfg := prConfig(base, ref, profile)
	pr, err := e.basics(ctx, ref)
	if err != nil {
		return err
	}
	if pr.State != dgh.StateOpen {
		return fmt.Errorf("%s is %s", ref.URL(), pr.State)
	}
	repos, err := e.WS.Repos(ctx)
	if err != nil {
		return err
	}
	all, err := e.WS.List(ctx)
	if err != nil {
		return err
	}
	remoteKey := ref.RemoteKey()
	ws, found := findReviewWorkspace(cfg, repos, all, remoteKey, ref.Number, pr.HeadRefName)
	facts := catalog.RoleFacts{ReviewWorkspace: found}
	if !found {
		if facts, err = e.heldRoleFacts(ctx, cfg, repos, all, ref, pr); err != nil {
			return err
		}
	}
	role := catalog.WorkspaceRole(facts)
	held, ok := catalog.ResolveRun(e.loadedIntents().Registry, role)
	if !ok {
		return fmt.Errorf("%s: no single intent drives a row from a %s workspace", ref, role)
	}
	if !found {
		return e.askedRound(ctx, held, base, ref, "", nil, profile)
	}

	rounds, err := e.rounds()
	if err != nil {
		return err
	}
	st, err := rounds.Load(ctx)
	if err != nil {
		return err
	}
	full, viewer, err := e.readPR(ctx, cfg, ref)
	if err != nil {
		return err
	}
	sent, err := e.sendTurn(ctx, cfg, turn.Request{Intent: turn.Intent(held.Name), Trigger: turn.Automated, Planned: turn.PlannedFollowUp, Unit: ref.Unit(), Payload: turnPR(pr)})
	if err != nil {
		return err
	}
	submitted, verb, err := e.startRound(ctx, cfg, ws, ref, &sent, intentOptions(held, roundOptions{allowUserActive: true, trusted: trusts(cfg, full), pr: full}))
	if err != nil {
		return fmt.Errorf("%s: %w", ws.DisplayName, err)
	}
	e.alignWithPR(ctx, ws, pr)
	if err := e.setReviewStatus(ctx, cfg, ref, ws, false); err != nil {
		fmt.Fprintf(e.Err, "warning: setting the board status: %v\n", err)
	}
	if !submitted {
		verb += " (submission not observed; check the session)"
	} else {
		e.recordReviewed(st, cfg, ref, ws, full, viewer)
	}
	fmt.Fprintf(e.Out, "follow-up in %s, %s: %s\n%s\n", ws.DisplayName, verb, pr.Title, ws.Path)
	return rounds.Save(ctx, st)
}

// fetchPR has the GitHub plugin read ref in full and report it, and
// returns it as the sweep reads it, with viewer, else the stored viewer,
// else GitHub's.
func (e *Env) fetchPR(ctx context.Context, _ config.Config, ref pr.Ref, viewer string) (*pr.PullRequest, string, error) {
	if e.Store == nil {
		return nil, "", errNoStore
	}
	if err := e.refreshPR(ctx, ref); err != nil {
		return nil, "", fmt.Errorf("read %s: %w", ref, err)
	}
	row, ok, err := e.Store.PR(ctx, ref)
	if err == nil && !ok {
		err = fmt.Errorf("read %s: the GitHub plugin reported no pull request", ref)
	}
	if err != nil {
		return nil, "", err
	}
	details, err := e.Store.Details(ctx, []pr.Ref{ref})
	if err != nil {
		return nil, "", err
	}
	d := details[ref]
	if viewer != "" {
		return inventory.GitHubOf(row, d, viewer), viewer, nil
	}
	viewer, err = e.Store.Viewer(ctx, unit.GitHub)
	if err == nil && viewer == "" {
		viewer, err = e.forgeViewer(ctx)
	}
	if err != nil {
		return nil, "", err
	}
	return inventory.GitHubOf(row, d, viewer), viewer, nil
}

// heldRoleFacts are the role facts of a pull request with no review
// workspace, read up to the first that makes it the author's.
func (e *Env) heldRoleFacts(ctx context.Context, cfg config.Config, repos []wsdomain.Repo, all []wsdomain.Workspace, ref pr.Ref, pr *pr.PullRequest) (catalog.RoleFacts, error) {
	var f catalog.RoleFacts
	var err error
	mine := e.prOf(ctx, pr)
	if _, f.AuthorWorkspace, err = findAuthorWorkspace(cfg, repos, all, ref, pr, mine); err != nil || f.AuthorWorkspace {
		return f, err
	}
	if f.Mine, err = mine(); err != nil || f.Mine {
		return f, err
	}
	overrides, err := e.Rec.Overrides(ctx)
	if err != nil {
		return f, err
	}
	f.Adopted = overrides[ref].Adopted
	return f, nil
}

func runBrief(ctx context.Context, e *Env, args []string) error {
	if len(args) != 2 || args[0] != "refresh" {
		return errors.New("usage: brief refresh <pr-url>")
	}
	ref, err := pr.ParseURL(args[1])
	if err != nil {
		return err
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	out, err := c.Action(ctx, ref.Unit(), "brief_refresh", api.ActionRequest{Source: sourceCLI})
	for _, l := range out {
		fmt.Fprintln(e.Out, l)
	}
	return err
}

// focusRunWorkspace brings on screen the workspace of u's newest run that ran in one.
func (e *Env) focusRunWorkspace(ctx context.Context, u unit.Unit) error {
	if e.Turns == nil {
		return fmt.Errorf("%s has no workspace to open", u.Label())
	}
	runs, err := e.Turns.RunsOf(ctx, u)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.Manager != "" && r.Workspace != "" {
			return e.WS.Focus(ctx, wsdomain.Workspace{Manager: r.Manager, ID: r.Workspace})
		}
	}
	return fmt.Errorf("%s has no workspace to open", u.Label())
}
