package cli

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"

	slacksource "github.com/wloophq/wloop/internal/adapter/source/slack"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/slack"
	"github.com/wloophq/wloop/internal/slack/auth"
	"github.com/wloophq/wloop/internal/slack/send"
	"github.com/wloophq/wloop/internal/source"
)

const slackPlugin unit.ProviderID = "slack"

// loginWait bounds how long slack login waits for the browser's callback.
const loginWait = 5 * time.Minute

// slackEnv is how the Slack commands and the daemon reach the Keychain, Slack
// and the browser; tests replace each.
type slackEnv struct {
	// Keychain nil is the login Keychain through security.
	Keychain auth.Keychain
	// BaseURL "" is slack.com.
	BaseURL string
	// LockPath "" is the token store's default.
	LockPath string
	// LoginPort nil is the config's slack.redirect_port.
	LoginPort *int
	// Open nil opens the URL in the default browser.
	Open func(url string) error
	// Interactive nil asks whether standard input is a terminal.
	Interactive func() bool
	// ReadSecret nil reads a line from the standard input terminal without echoing it.
	ReadSecret func() ([]byte, error)
	// Now nil is the wall clock.
	Now func() time.Time
}

// slackKit is one process's Slack client, token store and sender over one config.
type slackKit struct {
	Store  *auth.Store
	Client *slack.Client
	Send   *send.Service
}

func (s slackEnv) kit(cfg config.Slack, logf func(string, ...any)) *slackKit {
	kc := s.Keychain
	if kc == nil {
		kc = loginKeychain()
	}
	client := &slack.Client{BaseURL: s.BaseURL}
	if cfg.RequestLog && logf != nil {
		client.RequestLog = func(method string, status int) { logf("slack_call method=%s status=%d", method, status) }
	}
	store := &auth.Store{Keychain: kc, Service: cfg.Service(), LockPath: s.LockPath, Client: client, ClientID: cfg.ClientID, Now: s.Now, Log: logf}
	client.Tokens = store
	return &slackKit{Store: store, Client: client, Send: &send.Service{Client: client, Log: logf, PostAllowlist: cfg.PostAllowlist}}
}

// loginKeychain is the user's login Keychain, which a test binary never reaches.
func loginKeychain() auth.Keychain {
	if testing.Testing() {
		return refusedKeychain{}
	}
	return auth.SecurityKeychain{}
}

type refusedKeychain struct{}

var errTestKeychain = errors.New("tests never reach the login Keychain; set slackEnv.Keychain")

func (refusedKeychain) Get(string, string) ([]byte, error) { return nil, errTestKeychain }
func (refusedKeychain) Set(string, string, []byte) error   { return errTestKeychain }
func (refusedKeychain) Delete(string, string) error        { return errTestKeychain }

// withToken is a client that calls Slack with tok as is, never refreshing it.
func (k *slackKit) withToken(tok string) *slack.Client {
	return &slack.Client{BaseURL: k.Client.BaseURL, HTTP: k.Client.HTTP, RequestLog: k.Client.RequestLog, Tokens: fixedToken(tok)}
}

type fixedToken string

func (t fixedToken) Token(context.Context) (string, error) { return string(t), nil }

var slackUsage = "usage: " + brand.Command + " slack login [--no-browser] | login --app-token (reads standard input) | logout | status | send <target> <text> [--thread=false] [--broadcast] [--yes] | follow <message> | unfollow <message> | eval export|run|score|optimize ..."

func runSlack(ctx context.Context, e *Env, args []string) error {
	if len(args) == 0 {
		return usageError{usage: slackUsage}
	}
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	logf := func(format string, a ...any) { fmt.Fprintf(e.Err, format+"\n", a...) }
	kit := e.slack.kit(cfg.Slack, logf)
	switch args[0] {
	case "login":
		return e.slackLogin(ctx, cfg, kit, args[1:])
	case "logout":
		return e.slackLogout(ctx, kit, args[1:])
	case "status":
		return e.slackStatus(kit, args[1:])
	case "send":
		return e.slackSend(ctx, cfg, kit, args[1:])
	case "follow", "unfollow":
		return e.slackFollow(ctx, kit, args[0], args[1:])
	case "eval":
		return e.slackEval(ctx, cfg, args[1:])
	}
	return usageError{usage: slackUsage}
}

// slackModule builds the Slack plugin over the login Keychain and slack.com.
func slackModule(cfg config.Config, d source.Deps) (source.Module, error) {
	return slackEnv{}.module(cfg, d)
}

func (s slackEnv) module(cfg config.Config, d source.Deps) (source.Module, error) {
	kit := s.kit(cfg.Slack, d.Log)
	return slacksource.Module(cfg, d, slacksource.Slack{Tokens: kit.Store, Client: kit.Client})
}

// sourcePlugins are the compiled-in plugins with the Slack module over e's Slack environment.
func (e *Env) sourcePlugins() []source.Plugin {
	out := slices.Clone(plugins)
	for i := range out {
		if out[i].ID == slackPlugin {
			out[i].Build = e.slack.module
		}
	}
	return out
}

// slackFollow sends the track (follow) or untrack (unfollow) of the thread a
// message ref or permalink names: the parent's thread when it names a reply.
func (e *Env) slackFollow(ctx context.Context, kit *slackKit, verb string, args []string) error {
	if len(args) != 1 {
		return usageError{usage: slackUsage}
	}
	u, err := slackThreadOf(ctx, kit, args[0])
	if err != nil {
		return err
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	action := map[string]string{"follow": "track", "unfollow": "untrack"}[verb]
	out, err := c.Action(ctx, u, action, api.ActionRequest{Source: sourceCLI})
	printLines(e.Out, out)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.Out, "slack %s %s: done\n", verb, u.Key)
	return err
}

// slackMessageUnit is the unit a message ref or permalink lands in, as the
// daemon resolves it: the tracked direct message segment or thread it is in,
// else its thread.
func (e *Env) slackMessageUnit(ctx context.Context, message string) (unit.Unit, error) {
	cfg, err := e.loadConfig()
	if err != nil {
		return unit.Unit{}, err
	}
	if !cfg.PluginEnabled(string(slackPlugin)) {
		return unit.Unit{}, fmt.Errorf("%s names a Slack message, and the Slack plugin is disabled (slack.enabled is false)", message)
	}
	if _, err := e.slack.kit(cfg.Slack, nil).Store.Load(); errors.Is(err, auth.ErrNotLoggedIn) {
		return unit.Unit{}, fmt.Errorf("%w; run %q", err, brand.Command+" slack login")
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return unit.Unit{}, err
	}
	u, ok, err := c.ResolveUnit(ctx, message)
	switch {
	case err != nil:
		return unit.Unit{}, err
	case !ok || u.Provider != slackPlugin:
		return unit.Unit{}, fmt.Errorf("the daemon resolves no Slack unit for %s", message)
	}
	return u, nil
}

// slackThreadOf is the thread unit a message ref or permalink names in the logged-in team.
func slackThreadOf(ctx context.Context, kit *slackKit, message string) (unit.Unit, error) {
	tok, err := kit.Store.Load()
	if errors.Is(err, auth.ErrNotLoggedIn) {
		return unit.Unit{}, fmt.Errorf("%w; run %q", err, brand.Command+" slack login")
	}
	if err != nil {
		return unit.Unit{}, err
	}
	return slacksource.FollowUnit(ctx, kit.Client, tok.Team, message)
}

func (e *Env) slackNow() time.Time {
	if e.slack.Now != nil {
		return e.slack.Now()
	}
	return time.Now()
}

func (e *Env) slackLogin(ctx context.Context, cfg config.Config, kit *slackKit, args []string) error {
	fs := quietFlags("slack login")
	noBrowser := fs.Bool("no-browser", false, "print the login URL instead of opening the browser")
	appToken := fs.Bool("app-token", false, "save the app-level token read from standard input")
	for _, a := range args {
		if v, ok := flagValue(a, "app-token"); ok && v != "true" && v != "false" {
			return errors.New("slack login --app-token reads the token from standard input, never from the command line")
		}
	}
	if err := fs.Parse(args); err != nil {
		return slackFlagError(fs.Name(), err)
	}
	if fs.NArg() > 0 {
		if *appToken {
			return errors.New("slack login --app-token reads the token from standard input, never from the command line")
		}
		return usageError{usage: slackUsage}
	}
	if *appToken {
		return e.saveAppToken(kit)
	}
	if !cfg.Slack.Configured() {
		return fmt.Errorf("slack login needs slack.client_id in %s", e.ConfigPath)
	}

	port := cfg.Slack.Port()
	if e.slack.LoginPort != nil {
		port = *e.slack.LoginPort
	}
	open := e.slack.Open
	if open == nil {
		open = func(u string) error { return e.openBrowser(ctx, u) }
	}
	if *noBrowser {
		open = func(u string) error {
			_, err := fmt.Fprintf(e.Out, "Open this URL to log in to Slack:\n%s\n", u)
			return err
		}
	} else {
		fmt.Fprintln(e.Out, "Opening the browser to log in to Slack...")
	}
	lctx, cancel := context.WithTimeout(ctx, loginWait)
	defer cancel()
	tok, err := auth.Login{ClientID: cfg.Slack.ClientID, RedirectHost: "127.0.0.1", Port: port, Open: open, Client: kit.Client, Now: e.slackNow}.Run(lctx)
	if err != nil {
		return err
	}
	if id, err := kit.withToken(tok.Access).AuthTest(ctx); err != nil {
		fmt.Fprintf(e.Err, "auth.test failed (%v); saving the token without the team name\n", err)
	} else {
		tok.TeamName = id.TeamName
		tok.Team = cmp.Or(tok.Team, id.Team)
		tok.User = cmp.Or(tok.User, id.User)
		if len(tok.Scopes) == 0 {
			tok.Scopes = id.Scopes
		}
	}
	if err := kit.Store.Save(tok); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "Logged in to Slack as %s in %s; the token is in Keychain service %s.\n", tok.User, teamLabel(tok), kit.Store.Service)
	return nil
}

func (e *Env) openBrowser(ctx context.Context, u string) error {
	if testing.Testing() {
		return errors.New("tests never open a browser; set slackEnv.Open")
	}
	_, err := e.Sh(ctx, "open", u)
	return err
}

// flagValue is the value of a -name=value or --name=value argument.
func flagValue(arg, name string) (string, bool) {
	for _, prefix := range []string{"--" + name + "=", "-" + name + "="} {
		if v, ok := strings.CutPrefix(arg, prefix); ok {
			return v, true
		}
	}
	return "", false
}

func (e *Env) saveAppToken(kit *slackKit) error {
	in := e.In
	if in == nil {
		in = os.Stdin
	}
	var raw []byte
	var err error
	if e.slackInteractive() {
		fmt.Fprint(e.Err, "Paste the Slack app-level token (input hidden): ")
		raw, err = e.readSecret()
		fmt.Fprintln(e.Err)
	} else {
		raw, err = io.ReadAll(io.LimitReader(in, 4096))
	}
	if err != nil {
		return fmt.Errorf("reading the app token: %w", err)
	}
	tok := strings.TrimSpace(string(raw))
	if tok == "" {
		return errors.New("slack login --app-token: standard input is empty")
	}
	if err := kit.Store.SaveAppToken(tok); err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.Out, "Saved the Slack app token in Keychain service %s.\n", kit.Store.Service)
	return err
}

func teamLabel(tok auth.Token) string {
	if tok.TeamName == "" {
		return tok.Team
	}
	return tok.TeamName + " (" + tok.Team + ")"
}

func (e *Env) slackStatus(kit *slackKit, args []string) error {
	if len(args) > 0 {
		return usageError{usage: slackUsage}
	}
	tok, err := kit.Store.Load()
	if errors.Is(err, auth.ErrNotLoggedIn) {
		_, err := fmt.Fprintf(e.Out, "Slack: not logged in (Keychain service %s); run %q\n", kit.Store.Service, brand.Command+" slack login")
		return err
	}
	if err != nil {
		return err
	}
	now := e.slackNow()
	expires := "never"
	if !tok.Expires.IsZero() {
		expires = tok.Expires.Local().Format("2006-01-02 15:04 MST")
		if d := tok.Expires.Sub(now); d > 0 {
			expires += " (in " + roughDuration(d) + ")"
		} else {
			expires += " (expired; refreshed on the next call)"
		}
	}
	granted := "unknown"
	if !tok.Granted.IsZero() {
		granted = roughDuration(now.Sub(tok.Granted)) + " ago"
	}
	app := "stored"
	if _, err := kit.Store.AppToken(); errors.Is(err, auth.ErrNotLoggedIn) {
		app = "not stored"
	} else if err != nil {
		app = "unknown: " + err.Error()
	}
	_, err = fmt.Fprintf(e.Out, "Slack: logged in (Keychain service %s)\nteam: %s\nuser: %s\nexpires: %s\ngranted: %s\napp token: %s\n",
		kit.Store.Service, teamLabel(tok), tok.User, expires, granted, app)
	return err
}

// roughDuration is d in days and hours, or hours and minutes under a day.
func roughDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	days, hours, mins := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour), int(d%time.Hour/time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func (e *Env) slackLogout(ctx context.Context, kit *slackKit, args []string) error {
	if len(args) > 0 {
		return usageError{usage: slackUsage}
	}
	tok, err := kit.Store.Load()
	switch {
	case err == nil:
		if err := kit.withToken(tok.Access).Revoke(ctx); err != nil {
			fmt.Fprintf(e.Out, "Slack auth.revoke failed (%v); deleting the local tokens anyway.\n", err)
		} else {
			fmt.Fprintln(e.Out, "Revoked the Slack token.")
		}
	case errors.Is(err, auth.ErrNotLoggedIn):
		fmt.Fprintln(e.Out, "Not logged in to Slack; nothing to revoke.")
	default:
		fmt.Fprintf(e.Out, "Cannot read the Slack token (%v); skipped the revoke, deleting the local tokens anyway.\n", err)
	}
	if err := errors.Join(kit.Store.Delete(), kit.Store.DeleteAppToken()); err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.Out, "Deleted the Slack tokens from Keychain service %s.\n", kit.Store.Service)
	return err
}

func (e *Env) slackSend(ctx context.Context, cfg config.Config, kit *slackKit, args []string) error {
	fs := quietFlags("slack send")
	thread := fs.Bool("thread", true, "reply in the thread of a permalink target; --thread=false posts to its conversation")
	broadcast := fs.Bool("broadcast", false, "also send a thread reply to the conversation")
	yes := fs.Bool("yes", false, "post without asking")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return slackFlagError(fs.Name(), err)
	}
	if len(pos) != 2 {
		return usageError{usage: slackUsage}
	}
	if !cfg.PluginEnabled(string(slackPlugin)) {
		return errors.New("the Slack plugin is disabled (slack.enabled is false)")
	}
	req := send.Request{Target: pos[0], Text: pos[1], Broadcast: *broadcast, Source: "cli"}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "thread" {
			req.Thread = thread
		}
	})
	if strings.TrimSpace(req.Text) == "" {
		return errors.New("the message is empty")
	}
	if send.IsMessage(req.Target) && (req.Thread == nil || *req.Thread) && !req.Broadcast {
		return e.slackSendDraft(ctx, kit, req, *yes)
	}
	if !*yes {
		if err := e.confirmSlackSend(ctx, kit, req); err != nil {
			return err
		}
	}
	sent, err := kit.Send.Send(ctx, req)
	if err != nil {
		return slackLoginHint(err)
	}
	_, err = fmt.Fprintf(e.Out, "Posted to %s%s: %s\n", sent.Conversation, threadLabel(sent.Parent), sent.Permalink)
	return err
}

// slackSendDraft posts req, a reply in the thread of a message ref or permalink, through the Outbox: a draft of the user's
// on the thread's unit, approved once they say yes, or at once with yes.
func (e *Env) slackSendDraft(ctx context.Context, kit *slackKit, req send.Request, yes bool) error {
	u, err := e.slackMessageUnit(ctx, req.Target)
	if err != nil {
		return err
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	ready := func() error {
		if !e.slackInteractive() {
			return errors.New("slack send asks before posting; pass --yes when standard input is not a terminal")
		}
		return nil
	}
	cr := api.DraftCreate{Unit: u, Kind: api.DraftReply, Text: req.Text, Source: string(dturn.Terminal)}
	return e.sendUserDraft(ctx, c, cr, yes, ready, false)
}

func threadLabel(parent string) string {
	if parent == "" {
		return ""
	}
	return fmt.Sprintf(" in the thread %q", parent)
}

func slackLoginHint(err error) error {
	if errors.Is(err, send.ErrNotLoggedIn) {
		return fmt.Errorf("%w; run %q", err, brand.Command+" slack login")
	}
	return err
}

// confirmSlackSend shows where req goes and what it says, and asks.
func (e *Env) confirmSlackSend(ctx context.Context, kit *slackKit, req send.Request) error {
	if !e.slackInteractive() {
		return errors.New("slack send asks before posting; pass --yes when standard input is not a terminal")
	}
	res, err := kit.Send.Resolve(ctx, req)
	if err != nil {
		return slackLoginHint(err)
	}
	if err := kit.Send.Allowed(res); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "Post to %s%s:\n\n%s\n\nSend? [y/N] ", res.Conversation, threadLabel(res.Parent), send.Mrkdwn(req.Text))
	in := e.In
	if in == nil {
		in = os.Stdin
	}
	answer, _ := bufio.NewReader(in).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		fmt.Fprintln(e.Out, "Not sent.")
		return exitCode(1)
	}
	return nil
}

func (e *Env) readSecret() ([]byte, error) {
	if e.slack.ReadSecret != nil {
		return e.slack.ReadSecret()
	}
	f, ok := e.In.(*os.File)
	if !ok {
		f = os.Stdin
	}
	return term.ReadPassword(int(f.Fd()))
}

func (e *Env) slackInteractive() bool {
	if e.slack.Interactive != nil {
		return e.slack.Interactive()
	}
	f, ok := e.In.(*os.File)
	if e.In == nil {
		f, ok = os.Stdin, true
	}
	return ok && term.IsTerminal(int(f.Fd()))
}

// slackChecks are doctor's Slack lines; none while Slack is not configured.
func (e *Env) slackChecks(cfg config.Config) []check {
	if !cfg.Slack.Configured() {
		return nil
	}
	var out []check
	kit := e.slack.kit(cfg.Slack, nil)
	login := brand.Command + " slack login"
	tok, err := kit.Store.Load()
	switch {
	case errors.Is(err, auth.ErrNotLoggedIn):
		out = append(out, check{markMissing, "slack login", "not logged in (Keychain service " + kit.Store.Service + ")", login})
	case err != nil:
		out = append(out, check{markMissing, "slack login", err.Error(), ""})
	default:
		out = append(out, check{markOK, "slack login", tok.User + " in " + teamLabel(tok) + " (Keychain service " + kit.Store.Service + ")", ""})
	}
	if !cfg.PluginEnabled(string(slackPlugin)) {
		out = append(out, check{markInfo, "slack plugin", "disabled by slack.enabled; the Slack tools are off", ""})
	}
	if len(cfg.Slack.PostAllowlist) > 0 {
		out = append(out, check{markInfo, "slack post allowlist", strings.Join(cfg.Slack.PostAllowlist, ", "), ""})
	}
	return out
}

// quietFlags parses flags without printing: the flag package's messages
// quote the offending argument, which may be a pasted token.
func quietFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func slackFlagError(command string, err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return usageError{usage: slackUsage}
	}
	return usageError{usage: slackUsage, why: command + ": a flag is unknown or has a bad value (not repeated here, in case it holds a token)"}
}
