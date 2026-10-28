package slack

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/auth"
	"github.com/wloophq/wloop/plugins/slack/config"
)

// loginWait bounds how long slack login waits for the browser's callback.
const loginWait = 5 * time.Minute

// The commands the plugin binary serves in CLI mode.
const (
	CommandLogin  = "login"
	CommandLogout = "logout"
	CommandStatus = "status"
)

// exitUsage is the exit code of a command line the plugin does not take.
const exitUsage = 2

// RunCLI runs login, logout or status with no daemon and no store: they
// read and write only the Keychain and call Slack.
func (s *Plugin) RunCLI(ctx context.Context, req src.CLIRequest, stdout, stderr io.Writer) (int, error) {
	cfg, err := config.Parse(req.Config, brand())
	if err != nil {
		return 1, err
	}
	logf := func(format string, a ...any) { fmt.Fprintf(stderr, format+"\n", a...) }
	kit := s.Env.Kit(cfg, logf, nil)
	c := cli{env: s.Env, cfg: cfg, kit: kit, out: stdout, err: stderr, stdin: req.Stdin}
	if len(req.Args) == 0 {
		return c.usage()
	}
	args := req.Args[1:]
	switch req.Args[0] {
	case CommandLogin:
		err = c.login(ctx, args)
	case CommandLogout:
		err = c.logout(ctx, args)
	case CommandStatus:
		err = c.status(args)
	default:
		return c.usage()
	}
	var u usageError
	if errors.As(err, &u) {
		if u.why != "" {
			fmt.Fprintln(stderr, u.why)
		}
		return c.usage()
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

type usageError struct{ why string }

func (e usageError) Error() string { return "usage: " + e.why }

// cli is one command's environment.
type cli struct {
	env      Env
	cfg      config.Config
	kit      *Kit
	out, err io.Writer
	stdin    []byte
}

func (c cli) usage() (int, error) {
	fmt.Fprintf(c.err, "usage: %s slack login [--no-browser] | login --app-token (reads standard input) | logout | status\n", brand())
	return exitUsage, nil
}

func (c cli) now() time.Time {
	if c.env.Now != nil {
		return c.env.Now()
	}
	return time.Now()
}

// quietFlags parses flags without printing: the flag package's messages
// quote the offending argument, which may be a pasted token.
func quietFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func flagError(command string, err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return usageError{}
	}
	return usageError{why: command + ": a flag is unknown or has a bad value (not repeated here, in case it holds a token)"}
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

func (c cli) login(ctx context.Context, args []string) error {
	fs := quietFlags("slack login")
	noBrowser := fs.Bool("no-browser", false, "print the login URL instead of opening the browser")
	appToken := fs.Bool("app-token", false, "save the app-level token read from standard input")
	for _, a := range args {
		if v, ok := flagValue(a, "app-token"); ok && v != "true" && v != "false" {
			return errors.New("slack login --app-token reads the token from standard input, never from the command line")
		}
	}
	if err := fs.Parse(args); err != nil {
		return flagError(fs.Name(), err)
	}
	if fs.NArg() > 0 {
		if *appToken {
			return errors.New("slack login --app-token reads the token from standard input, never from the command line")
		}
		return usageError{}
	}
	if *appToken {
		return c.saveAppToken()
	}
	if !c.cfg.Slack.Configured() {
		return errors.New("slack login needs plugins.slack.client_id in the configuration")
	}
	port := c.cfg.Slack.Port()
	if c.env.LoginPort != nil {
		port = *c.env.LoginPort
	}
	open := func(u string) error { return c.openBrowser(ctx, u) }
	if *noBrowser {
		open = func(u string) error {
			_, err := fmt.Fprintf(c.out, "Open this URL to log in to Slack:\n%s\n", u)
			return err
		}
	} else {
		fmt.Fprintln(c.out, "Opening the browser to log in to Slack...")
	}
	lctx, cancel := context.WithTimeout(ctx, loginWait)
	defer cancel()
	tok, err := auth.Login{ClientID: c.cfg.Slack.ClientID, RedirectHost: "127.0.0.1", Port: port, Open: open, Client: c.kit.Client, Now: c.now}.Run(lctx)
	if err != nil {
		return err
	}
	if id, err := c.kit.withToken(tok.Access).AuthTest(ctx); err != nil {
		fmt.Fprintf(c.err, "auth.test failed (%v); saving the token without the team name\n", err)
	} else {
		tok.TeamName = id.TeamName
		tok.Team = cmp.Or(tok.Team, id.Team)
		tok.User = cmp.Or(tok.User, id.User)
		if len(tok.Scopes) == 0 {
			tok.Scopes = id.Scopes
		}
	}
	if err := c.kit.Store.Save(tok); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Logged in to Slack as %s in %s; the token is in Keychain service %s.\n", tok.User, teamLabel(tok), c.kit.Store.Service)
	return nil
}

func (c cli) openBrowser(ctx context.Context, u string) error {
	if c.env.Open != nil {
		return c.env.Open(ctx, u)
	}
	return exec.CommandContext(ctx, "open", u).Run()
}

func (c cli) saveAppToken() error {
	tok := strings.TrimSpace(string(c.stdin))
	if tok == "" {
		return errors.New("slack login --app-token: standard input is empty")
	}
	if err := c.kit.Store.SaveAppToken(tok); err != nil {
		return err
	}
	_, err := fmt.Fprintf(c.out, "Saved the Slack app token in Keychain service %s.\n", c.kit.Store.Service)
	return err
}

func teamLabel(tok auth.Token) string {
	if tok.TeamName == "" {
		return tok.Team
	}
	return tok.TeamName + " (" + tok.Team + ")"
}

func (c cli) status(args []string) error {
	if len(args) > 0 {
		return usageError{}
	}
	tok, err := c.kit.Store.Load()
	if errors.Is(err, auth.ErrNotLoggedIn) {
		_, err := fmt.Fprintf(c.out, "Slack: not logged in (Keychain service %s); run %q\n", c.kit.Store.Service, brand()+" slack login")
		return err
	}
	if err != nil {
		return err
	}
	now := c.now()
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
	if _, err := c.kit.Store.AppToken(); errors.Is(err, auth.ErrNotLoggedIn) {
		app = "not stored"
	} else if err != nil {
		app = "unknown: " + err.Error()
	}
	_, err = fmt.Fprintf(c.out, "Slack: logged in (Keychain service %s)\nteam: %s\nuser: %s\nexpires: %s\ngranted: %s\napp token: %s\n",
		c.kit.Store.Service, teamLabel(tok), tok.User, expires, granted, app)
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

func (c cli) logout(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return usageError{}
	}
	tok, err := c.kit.Store.Load()
	switch {
	case err == nil:
		if err := c.kit.withToken(tok.Access).Revoke(ctx); err != nil {
			fmt.Fprintf(c.out, "Slack auth.revoke failed (%v); deleting the local tokens anyway.\n", err)
		} else {
			fmt.Fprintln(c.out, "Revoked the Slack token.")
		}
	case errors.Is(err, auth.ErrNotLoggedIn):
		fmt.Fprintln(c.out, "Not logged in to Slack; nothing to revoke.")
	default:
		fmt.Fprintf(c.out, "Cannot read the Slack token (%v); skipped the revoke, deleting the local tokens anyway.\n", err)
	}
	if err := errors.Join(c.kit.Store.Delete(), c.kit.Store.DeleteAppToken()); err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.out, "Deleted the Slack tokens from Keychain service %s.\n", c.kit.Store.Service)
	return err
}
