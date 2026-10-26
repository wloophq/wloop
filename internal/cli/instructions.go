package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/hook"
	"github.com/wloophq/wloop/internal/client/turncli"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/shell"
	"github.com/wloophq/wloop/internal/profile"
)

// loopCommand is how a turn calls the loop: the binary by its absolute
// path, which works whatever the agent's PATH holds.
func (e *Env) loopCommand() string {
	if e.Executable == "" {
		return brand.Name
	}
	return shell.Quote(e.Executable)
}

// instructionsUsage is the usage of instructions with the registry's intents.
func instructionsUsage(reg *dintent.Registry) string {
	var byHand, stored []string
	for _, d := range reg.All() {
		stored = append(stored, string(d.Name))
		if d.UserFacing {
			byHand = append(byHand, string(d.Name))
		}
	}
	return "usage: instructions " + strings.Join(byHand, "|") + " [<target>] [<intent flags>] [--agent <name>] [-- <guidance...>]\n" +
		"       instructions " + strings.Join(stored, "|") + " --turn <id> [<pr-url>]"
}

func runInstructions(ctx context.Context, e *Env, args []string) error {
	reg := e.loadedIntents().Registry
	usage := instructionsUsage(reg)
	if len(args) == 0 {
		return errors.New(usage)
	}
	before, guidance := splitGuidance(args[1:])
	if slices.ContainsFunc(before, func(a string) bool {
		return a == "--turn" || strings.HasPrefix(a, "--turn=") || a == "-turn" || strings.HasPrefix(a, "-turn=")
	}) {
		return e.storedInstructions(ctx, reg, args[0], before, guidance != "" || slices.Contains(args[1:], "--"), usage)
	}
	d, err := e.intentDef(args[0])
	if err == nil && !d.UserFacing {
		err = fmt.Errorf("unknown intent %q; use %s", args[0], reg.Names())
	}
	if err != nil {
		return fmt.Errorf("%w\n%s", err, usage)
	}
	fs := flag.NewFlagSet("instructions", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	agentName := fs.String("agent", "", "the agent that reads them (default: the configured one)")
	inputs := map[string]any{}
	var flagged []dintent.Input
	for _, in := range d.Inputs {
		if in.From != "user" || in.Flag == "" || !slices.Contains(in.Surfaces, surfaceInstructions) {
			continue
		}
		flagged = append(flagged, in)
		name := strings.TrimPrefix(in.Flag, "--")
		if in.Type == "text" {
			fs.Func(name, in.Help, func(v string) error { inputs[in.Name] = v; return nil })
		} else {
			fs.BoolFunc(name, in.Help, func(string) error { inputs[in.Name] = true; return nil })
		}
	}
	if err := refuseForeignFlags(reg, d, before, flagged); err != nil {
		return err
	}
	pos, err := parseInterspersed(fs, before)
	if errors.Is(err, flag.ErrHelp) {
		return errors.New(usage)
	}
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return fmt.Errorf("one target at most\n%s", usage)
	}
	var url string
	if len(pos) == 1 {
		url = pos[0]
	}
	checked, err := daemon.CheckInputs(reg, d, inputs, surfaceInstructions, "", false)
	if err != nil {
		return errors.New(err.Error())
	}
	var cwd string
	if e.Harness.Cwd != nil {
		cwd = e.Harness.Cwd()
	}
	if cwd == "" {
		return errors.New("instructions cannot read its working directory")
	}
	_, pane, agentSession, home := e.caller()
	return e.askTurnByHand(ctx, api.ByHandRequest{Intent: string(d.Name), URL: url, Inputs: checked, Agent: *agentName, Guidance: guidance, Cwd: cwd,
		Pane: pane, AgentSession: agentSession, Home: home})
}

// storedInstructions starts the turn the loop stored under its id, of any
// intent of the registry, on the caller's session and prints it; it takes
// no input and no guidance.
func (e *Env) storedInstructions(ctx context.Context, reg *dintent.Registry, intent string, args []string, guided bool, usage string) error {
	if _, ok := reg.Get(dintent.Name(intent)); !ok {
		return fmt.Errorf("unknown intent %q; use %s\n%s", intent, reg.AllNames(), usage)
	}
	fs := flag.NewFlagSet("instructions", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	var id string
	fs.StringVar(&id, "turn", "", "start the turn "+brand.Name+" sent with this id and print its instructions")
	fs.String("agent", "", "the agent that reads them (default: the configured one)")
	var given []string
	for _, d := range reg.All() {
		for _, in := range d.Inputs {
			name := strings.TrimPrefix(in.Flag, "--")
			if in.Flag == "" || fs.Lookup(name) != nil {
				continue
			}
			set := func(string) error { given = append(given, in.Flag); return nil }
			if in.Type == "text" {
				fs.Func(name, in.Help, set)
			} else {
				fs.BoolFunc(name, in.Help, set)
			}
		}
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	switch {
	case len(pos) > 1:
		return fmt.Errorf("one target at most\n%s", usage)
	case id == "":
		return errors.New("--turn needs the id of a turn")
	case len(given) > 0:
		return fmt.Errorf("--turn prints a stored turn; it takes no %s and no guidance", given[0])
	case guided:
		return errors.New("--turn prints a stored turn; it takes no input flags and no guidance")
	}
	return e.startStoredTurn(ctx, intent, id)
}

// refuseForeignFlags refuses, before the flags are parsed, a flag d does not
// take that another intent declares.
func refuseForeignFlags(reg *dintent.Registry, d dintent.Def, args []string, own []dintent.Input) error {
	takesValue := map[string]bool{"--agent": true, "-agent": true}
	for _, in := range own {
		if in.Type == "text" {
			takesValue[in.Flag], takesValue[strings.TrimPrefix(in.Flag, "-")] = true, true
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name, _, hasValue := strings.Cut(a, "=")
		if takesValue[name] {
			if !hasValue {
				i++
			}
			continue
		}
		if slices.ContainsFunc(own, func(in dintent.Input) bool { return in.Flag == name }) {
			continue
		}
		if name == "-h" || name == "--help" || name == "-help" {
			continue
		}
		return flagError(reg, d, name)
	}
	return nil
}

// splitGuidance cuts the arguments at the first "--"; what follows is the
// user's guidance.
func splitGuidance(args []string) ([]string, string) {
	if i := slices.Index(args, "--"); i >= 0 {
		return args[:i], strings.Join(args[i+1:], " ")
	}
	return args, ""
}

// startStoredTurn starts the turn the loop sent with id on the caller's
// session and prints its instructions.
func (e *Env) startStoredTurn(ctx context.Context, intent, id string) error {
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	t, err := c.StartTurn(ctx, e.sessionHandle(), api.TurnStartRequest{Turn: id, Intent: intent})
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(e.Out, t.Text)
	return err
}

// askTurnByHand has the daemon run the turn in the caller's session: its
// instructions go to standard output, what the loop did around it to standard
// error.
func (e *Env) askTurnByHand(ctx context.Context, r api.ByHandRequest) error {
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	res, err := c.TurnByHand(ctx, e.sessionHandle(), r)
	for _, n := range res.Notes {
		fmt.Fprintln(e.Err, n)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(e.Out, res.Text)
	return err
}

func runHook(ctx context.Context, e *Env, args []string) int {
	const usage = "usage: hook session-start | hook stop | hook prompt [--agent <kind>] | hook mcp-headers"
	if len(args) == 0 {
		fmt.Fprintln(e.Err, usage)
		return 0
	}
	if args[0] == "mcp-headers" {
		return hook.MCPHeaders(e.Getenv, e.Out, e.Harness)
	}
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	kind := fs.String("agent", "", "the agent whose hook this is")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(e.Err, usage)
		return 0
	}
	c := e.agentClient()
	switch args[0] {
	case "session-start":
		return hook.SessionStart(ctx, e.Getenv, os.Stdin, e.Err, c, e.Harness, *kind)
	case "stop":
		return hook.Stop(ctx, e.Getenv, os.Stdin, e.Out, e.Err, c, e.Harness, *kind)
	case "prompt":
		return hook.Prompt(ctx, e.Getenv, os.Stdin, e.Err, c, e.Harness, *kind)
	}
	fmt.Fprintln(e.Err, usage)
	return 0
}

// caller is the calling session's identity, as its workspace manager and
// its agent set it in the environment.
func (e *Env) caller() (handle, pane, agentSession, home string) {
	if s := e.Harness.Session(e.Getenv); s != nil {
		handle, pane = s.Handle(e.Getenv), s.Pane(e.Getenv)
	}
	if p := e.Harness.Detect(e.Getenv); p != nil {
		agentSession, home = p.SessionID(e.Getenv), p.Home(e.Getenv)
	}
	return handle, pane, agentSession, home
}

func (e *Env) sessionHandle() string {
	handle, _, _, _ := e.caller()
	return handle
}

func runTurn(ctx context.Context, e *Env, args []string) int {
	c := e.agentClient()
	if len(args) > 0 {
		switch args[0] {
		case "end":
			return turncli.End(ctx, e.Getenv, args[1:], e.Out, e.Err, c, e.Harness)
		case "answer":
			return turncli.Answer(ctx, e.Getenv, args[1:], e.Out, e.Err, c, e.Harness)
		case "show":
			if err := runTurnShow(ctx, e, args[1:]); err != nil {
				fmt.Fprintf(e.Err, "%s error: %v\n", e.stamp(), err)
				return 1
			}
			return 0
		}
	}
	fmt.Fprintln(e.Err, "usage: turn end <pr-url> --turn <id> --file <path> | turn answer <pr-url> | turn show <pr-url> [--json] [--color auto|always|never]")
	return 2
}

// roundReady says whether the profile's blocks load, before any workspace
// or session is touched for a round.
func (e *Env) roundReady() error {
	return profile.Guides{Tree: e.loadedIntents().Tree}.Check()
}

// agentClient is a client of the daemon that names, with each call for the
// calling agent session, the manager that runs it.
func (e *Env) agentClient() *client.Client {
	c := client.New(e.Socket)
	c.Manager, _ = e.Harness.Manager(e.Getenv)
	return c
}
