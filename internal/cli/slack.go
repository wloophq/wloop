package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/source"
)

const slackPlugin unit.ProviderID = "slack"

// The Slack plugin's commands the host runs in the plugin binary, and the
// actions it asks of the running plugin.
const (
	slackLogin  = "login"
	slackLogout = "logout"
	slackStatus = "status"

	slackActionSend   = "send"
	slackActionThread = "thread"
	slackActionEval   = "eval"
)

// slackEnv is how the Slack commands reach the terminal; tests replace each.
type slackEnv struct {
	// Interactive nil asks whether standard input is a terminal.
	Interactive func() bool
	// ReadSecret nil reads a line from the standard input terminal without echoing it.
	ReadSecret func() ([]byte, error)
}

var slackUsage = "usage: " + brand.Command + " slack login [--no-browser] | login --app-token (reads standard input) | logout | status | send <target> <text> [--thread=false] [--broadcast] [--yes] | follow <message> | unfollow <message> | eval export|run|score|optimize ..."

func runSlack(ctx context.Context, e *Env, args []string) error {
	if len(args) == 0 {
		return usageError{usage: slackUsage}
	}
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	switch args[0] {
	case slackLogin:
		return e.slackLogin(ctx, cfg, args[1:])
	case slackLogout, slackStatus:
		return e.runPluginBinary(ctx, cfg, string(slackPlugin), args, nil)
	case "send":
		return e.slackSend(ctx, cfg, args[1:])
	case "follow", "unfollow":
		return e.slackFollow(ctx, args[0], args[1:])
	case "eval":
		return e.slackEval(ctx, args[1:])
	}
	return usageError{usage: slackUsage}
}

// sourcePlugins are the compiled-in plugins with the Slack module relaying
// to e's Slack plugin process.
func (e *Env) sourcePlugins() []source.Plugin {
	out := slices.Clone(plugins)
	for i := range out {
		if out[i].ID == slackPlugin {
			out[i].Build = e.slackRelay
		}
	}
	return out
}

// slackLogin runs the plugin's login; with --app-token the host reads the
// token, hidden at a terminal, and hands it to the plugin as its input.
func (e *Env) slackLogin(ctx context.Context, cfg config.Config, args []string) error {
	var stdin []byte
	if slices.ContainsFunc(args, isAppTokenFlag) {
		raw, err := e.readAppToken()
		if err != nil {
			return err
		}
		stdin = raw
	}
	return e.runPluginBinary(ctx, cfg, string(slackPlugin), append([]string{slackLogin}, args...), stdin)
}

func isAppTokenFlag(arg string) bool {
	return slices.Contains([]string{"--app-token", "-app-token", "--app-token=true", "-app-token=true"}, arg)
}

// readAppToken reads the app token: hidden at a terminal, else standard
// input.
func (e *Env) readAppToken() ([]byte, error) {
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
		return nil, fmt.Errorf("reading the app token: %w", err)
	}
	return raw, nil
}

// slackThread is the thread unit a message ref or permalink names, as the
// running plugin reads it: the parent's thread when it names a reply.
func (e *Env) slackThread(ctx context.Context, message string) (unit.Unit, error) {
	c, err := e.daemonClient(ctx)
	if err != nil {
		return unit.Unit{}, err
	}
	var w struct{ Provider, Key string }
	if err := c.PluginAction(ctx, string(slackPlugin), slackActionThread, map[string]string{"message": message}, &w); err != nil {
		return unit.Unit{}, err
	}
	return e.unitCodecs().Parse(w.Provider, w.Key)
}

// slackFollow sends the track (follow) or untrack (unfollow) of the thread a
// message ref or permalink names.
func (e *Env) slackFollow(ctx context.Context, verb string, args []string) error {
	if len(args) != 1 {
		return usageError{usage: slackUsage}
	}
	u, err := e.slackThread(ctx, args[0])
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

// pluginAddressUnit is the unit of plugin id an address lands in, as the
// daemon resolves it.
func (e *Env) pluginAddressUnit(ctx context.Context, id unit.ProviderID, address string) (unit.Unit, error) {
	cfg, err := e.loadConfig()
	if err != nil {
		return unit.Unit{}, err
	}
	if !cfg.PluginEnabled(string(id)) {
		return unit.Unit{}, fmt.Errorf("%s is an address of the %s plugin, which is disabled (plugins.%s.enabled is false)", address, id, id)
	}
	resolve := e.resolve
	if resolve == nil {
		c, err := e.daemonClient(ctx)
		if err != nil {
			return unit.Unit{}, err
		}
		resolve = c.ResolveUnit
	}
	u, ok, err := resolve(ctx, address)
	switch {
	case err != nil:
		return unit.Unit{}, err
	case !ok || u.Provider != id:
		return unit.Unit{}, fmt.Errorf("the daemon resolves no %s unit for %s: is its %s plugin running and logged in? Restart the daemon after enabling it", id, address, id)
	}
	return u, nil
}

// slackSendArgs are the plugin's send action's arguments.
type slackSendArgs struct {
	Target    string `json:"target"`
	Text      string `json:"text"`
	Thread    *bool  `json:"thread,omitempty"`
	Broadcast bool   `json:"broadcast,omitempty"`
	DryRun    bool   `json:"dry_run,omitempty"`
}

// slackSent is where the plugin's send action posted, or would post.
type slackSent struct {
	Conversation string `json:"conversation"`
	Parent       string `json:"parent"`
	Text         string `json:"text"`
	Permalink    string `json:"permalink"`
}

func (e *Env) slackSend(ctx context.Context, cfg config.Config, args []string) error {
	fs := quietFlags("slack send")
	thread := fs.Bool("thread", true, "reply in the thread of a message ref or permalink target; --thread=false posts to its conversation")
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
		return errors.New("the Slack plugin is disabled (plugins.slack.enabled is false)")
	}
	req := slackSendArgs{Target: pos[0], Text: pos[1], Broadcast: *broadcast}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "thread" {
			req.Thread = thread
		}
	})
	if strings.TrimSpace(req.Text) == "" {
		return errors.New("the message is empty")
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	if (req.Thread == nil || *req.Thread) && !req.Broadcast {
		u, ok, err := c.ResolveUnit(ctx, req.Target)
		if err != nil {
			return err
		}
		if ok && u.Provider == slackPlugin {
			return e.slackSendDraft(ctx, c, u, req, *yes)
		}
	}
	if !*yes {
		if err := e.confirmSlackSend(ctx, req); err != nil {
			return err
		}
	}
	var sent slackSent
	if err := c.PluginAction(ctx, string(slackPlugin), slackActionSend, req, &sent); err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.Out, "Posted to %s%s: %s\n", sent.Conversation, threadLabel(sent.Parent), sent.Permalink)
	return err
}

// slackSendDraft posts req, a reply in the thread of a message ref or permalink, through the Outbox: a draft of the user's
// on u, the message's unit, approved once they say yes, or at once with yes.
func (e *Env) slackSendDraft(ctx context.Context, c *client.Client, u unit.Unit, req slackSendArgs, yes bool) error {
	ready := func() error {
		if !e.slackInteractive() {
			return errors.New("slack send asks before posting; pass --yes when standard input is not a terminal")
		}
		return nil
	}
	cr := api.DraftCreate{Unit: u, Kind: api.DraftReply, Text: req.Text, Target: req.Target, Source: string(dturn.Terminal)}
	return e.sendUserDraft(ctx, c, cr, yes, ready, false)
}

func threadLabel(parent string) string {
	if parent == "" {
		return ""
	}
	return fmt.Sprintf(" in the thread %q", parent)
}

// confirmSlackSend shows where req goes and what it says, as the plugin's
// dry run answers, and asks.
func (e *Env) confirmSlackSend(ctx context.Context, req slackSendArgs) error {
	if !e.slackInteractive() {
		return errors.New("slack send asks before posting; pass --yes when standard input is not a terminal")
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	dry := req
	dry.DryRun = true
	var res slackSent
	if err := c.PluginAction(ctx, string(slackPlugin), slackActionSend, dry, &res); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "Post to %s%s:\n\n%s\n\nSend? [y/N] ", res.Conversation, threadLabel(res.Parent), res.Text)
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

// slackEvalPathFlags are the flags of slack eval that name a file or a
// directory, which the running plugin reads from its own directory.
var slackEvalPathFlags = []string{"out", "exclude", "fixtures", "prompt", "new"}

// slackEval runs slack eval in the running plugin, with its paths made
// absolute.
func (e *Env) slackEval(ctx context.Context, args []string) error {
	args, err := absPathFlags(args, slackEvalPathFlags)
	if err != nil {
		return err
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	var res struct{ Output, Log string }
	if err := c.PluginAction(ctx, string(slackPlugin), slackActionEval, map[string][]string{"args": args}, &res); err != nil {
		return err
	}
	fmt.Fprint(e.Err, res.Log)
	_, err = fmt.Fprint(e.Out, res.Output)
	return err
}

// absPathFlags is args with the value of each flag of names made absolute,
// as -name value, -name=value or their -- forms.
func absPathFlags(args, names []string) ([]string, error) {
	out := slices.Clone(args)
	isPath := func(a string) (string, bool) {
		n := strings.TrimLeft(a, "-")
		return n, n != a && slices.Contains(names, n)
	}
	for i := 0; i < len(out); i++ {
		if name, value, ok := strings.Cut(out[i], "="); ok {
			if n, path := isPath(name); path && value != "" {
				abs, err := filepath.Abs(value)
				if err != nil {
					return nil, err
				}
				out[i] = name[:len(name)-len(n)] + n + "=" + abs
			}
			continue
		}
		if _, path := isPath(out[i]); path && i+1 < len(out) {
			abs, err := filepath.Abs(out[i+1])
			if err != nil {
				return nil, err
			}
			out[i+1] = abs
			i++
		}
	}
	return out, nil
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

// slackStatusText is what the plugin's status command says.
func (e *Env) slackStatusText(ctx context.Context, cfg config.Config) (string, error) {
	var out bytes.Buffer
	err := e.runPluginBinaryTo(ctx, cfg, string(slackPlugin), []string{slackStatus}, nil, &out)
	return strings.TrimSpace(out.String()), err
}

// slackChecks are doctor's Slack lines: the plugin's status; none while
// Slack is not configured.
func (e *Env) slackChecks(ctx context.Context, cfg config.Config) []check {
	if !cfg.Slack.Configured() {
		return nil
	}
	var out []check
	status, err := e.slackStatusText(ctx, cfg)
	if err != nil {
		out = append(out, check{markMissing, "slack login", err.Error(), brand.Command + " slack login"})
	} else {
		out = append(out, check{markInfo, "slack login", strings.ReplaceAll(status, "\n", "; "), ""})
	}
	if !cfg.PluginEnabled(string(slackPlugin)) {
		out = append(out, check{markInfo, "slack plugin", "disabled by plugins.slack.enabled; the Slack tools are off", ""})
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

// slackSteps revoke and delete the stored Slack tokens through the Slack
// plugin's logout; none while its plugin is not installed. Only the plugin
// knows whether it holds tokens, so they go with the other integrations.
func (e *Env) slackSteps(cfg config.Config) []uninstallStep {
	if _, err := e.pluginBinary(string(slackPlugin)); err != nil {
		return nil
	}
	return []uninstallStep{{plan: []string{"the Slack tokens, revoked first by the Slack plugin's logout"}, run: func(ctx context.Context) error {
		return e.runPluginBinary(ctx, cfg, string(slackPlugin), []string{slackLogout}, nil)
	}}}
}
