package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/shell"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

// The input surfaces of the command line: an intent's own command and a
// turn by hand.
const (
	surfaceCLI          = "cli"
	surfaceInstructions = "instructions"
)

// The flags of an intake intent's command, which no manifest declares.
const (
	flagNoWait  = "--no-wait"
	flagNoBrief = "--no-brief"
)

// intentArgs is what an intent's command line asked for.
type intentArgs struct {
	inputs          map[string]any
	noWait, noBrief bool
	guidance        string
}

// runIntent runs an intent's own command: the daemon accepts an intake
// intent's round as an intake this command follows, and runs any other at
// once.
func runIntent(ctx context.Context, e *Env, d dintent.Def, args []string) error {
	usage := intentUsage(d)
	if len(args) == 0 {
		return errors.New(usage)
	}
	ref, err := github.ParseURL(args[0])
	if err != nil {
		u, uerr := e.unitTarget(ctx, args[0])
		switch {
		case uerr != nil && strings.Contains(args[0], "://") && !isSlackPermalink(args[0]):
			return err
		case uerr != nil:
			return uerr
		case isPR(u):
			ref = prOf(u)
		default:
			return runIntentOn(ctx, e, d, u, args[1:])
		}
	}
	reg := e.loadedIntents().Registry
	p, err := parseIntentArgs(reg, d, surfaceCLI, usage, args[1:])
	if err != nil {
		return err
	}
	req := api.ActionRequest{Guidance: p.guidance, NoBrief: p.noBrief, Inputs: p.inputs}
	if beforeSet(d, p.inputs, beforeTakeOver) {
		if err := e.action(ctx, ref, beforeTakeOver, api.ActionRequest{}, "done by the daemon"); err != nil && !ownedAlready(err, ref) {
			return err
		}
	}
	if d.Intake {
		err = e.ask(ctx, ref, string(d.Name), req, p.noWait)
	} else {
		err = e.action(ctx, ref, string(d.Name), req, "sent")
	}
	return takeOverHint(err, d, ref, p.inputs)
}

// runIntentOn runs d on u, a unit of a provider other than GitHub that d serves: headless when d's
// manifest runs it so, else in a session where u's placement says.
func runIntentOn(ctx context.Context, e *Env, d dintent.Def, u unit.Unit, args []string) error {
	if !d.Serves(u.Provider) {
		return fmt.Errorf("%s serves %s units, not %s ones like %s", d.Name, d.ProviderOrDefault(), u.Provider, u.Label())
	}
	p, err := parseIntentArgs(e.loadedIntents().Registry, d, surfaceCLI, intentUsage(d), args)
	if err != nil {
		return err
	}
	if p.noWait || p.noBrief || beforeSet(d, p.inputs, beforeTakeOver) {
		return fmt.Errorf("%s, %s and taking over apply to pull requests only", flagNoWait, flagNoBrief)
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	req := api.ActionRequest{Guidance: p.guidance, Inputs: p.inputs, Source: sourceCLI, AgentHome: e.callerAgentHome(config.Config{Agent: e.callerKind()})}
	res, err := c.Ask(ctx, u, string(d.Name), req)
	printLines(e.Out, res.Output)
	if err != nil {
		return err
	}
	if res.Seq > 0 {
		fmt.Fprintf(e.Out, "%s %s: started headless as turn %d; what it drafts waits in the Outbox (%s outbox)\n", d.Name, u.Label(), res.Seq, brand.Command)
		return nil
	}
	fmt.Fprintf(e.Out, "%s %s: sent\n", d.Name, u.Label())
	return nil
}

// ownedAlready tells a take-over refused because the pull request is the
// user's to develop already: taken over before, or theirs.
func ownedAlready(err error, ref github.Ref) bool {
	msg := err.Error()
	return strings.HasSuffix(msg, catalog.ReasonYours) || strings.HasSuffix(msg, daemon.NotOffered(beforeTakeOver, ref.Unit()))
}

// parseIntentArgs reads d's flags up to the first word that is not one, or
// "--"; every word after it is guidance, a leading dash included. A flag of
// another intent, an unknown one and an input the surface does not take are
// refused before any guidance is read.
func parseIntentArgs(reg *dintent.Registry, d dintent.Def, surface, usage string, args []string) (intentArgs, error) {
	var p intentArgs
	inputs := map[string]any{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" || !strings.HasPrefix(a, "-") {
			if a == "--" {
				i++
			}
			p.guidance = strings.Join(args[i:], " ")
			break
		}
		name, value, hasValue := strings.Cut(a, "=")
		switch name {
		case flagNoWait, flagNoBrief:
			if !d.Intake {
				return p, flagError(reg, d, name)
			}
			if hasValue {
				return p, fmt.Errorf("%s takes no value", name)
			}
			p.noWait, p.noBrief = p.noWait || name == flagNoWait, p.noBrief || name == flagNoBrief
			continue
		}
		in, ok := inputOfFlag(d, name)
		if !ok {
			return p, flagError(reg, d, name)
		}
		if !slices.Contains(in.Surfaces, surface) {
			return p, fmt.Errorf("%s is not accepted from %s", name, surface)
		}
		switch {
		case in.Type != "text" && hasValue:
			return p, fmt.Errorf("%s takes no value", name)
		case in.Type != "text":
			inputs[in.Name] = true
		case hasValue:
			inputs[in.Name] = value
		case i+1 < len(args):
			i++
			inputs[in.Name] = args[i]
		default:
			return p, fmt.Errorf("%s needs a value\n%s", name, usage)
		}
	}
	checked, err := daemon.CheckInputs(reg, d, inputs, surface, "", false)
	if err != nil {
		return p, errors.New(err.Error())
	}
	p.inputs = checked
	return p, nil
}

// inputOfFlag is d's input the user sets with flag.
func inputOfFlag(d dintent.Def, flag string) (dintent.Input, bool) {
	i := slices.IndexFunc(d.Inputs, func(in dintent.Input) bool { return in.From == "user" && in.Flag == flag })
	if i < 0 {
		return dintent.Input{}, false
	}
	return d.Inputs[i], true
}

// flagError says why d takes no flag: the intents that declare it, else that
// no intent does.
func flagError(reg *dintent.Registry, d dintent.Def, flag string) error {
	var owners []string
	if flag == flagNoWait || flag == flagNoBrief {
		for _, o := range reg.UserFacing() {
			if o.Intake {
				owners = append(owners, string(o.Name))
			}
		}
	} else {
		for _, n := range reg.DeclaringFlag(flag) {
			if n != d.Name {
				owners = append(owners, string(n))
			}
		}
	}
	if len(owners) == 0 {
		return fmt.Errorf("unknown flag %s", flag)
	}
	return fmt.Errorf("%s applies to %s", flag, dintent.OrList(owners))
}

// takeOverHint words the refusal of a pull request that is not the user's
// with the command that takes it over first and asks again with the same
// persisted flags.
func takeOverHint(err error, d dintent.Def, ref github.Ref, inputs map[string]any) error {
	why := actionRefusal(err, d)
	if why == "" {
		return err
	}
	var takeOver string
	var flags []string
	for _, in := range d.Inputs {
		switch {
		case in.Before == beforeTakeOver:
			takeOver = in.Flag
		case in.Persist && in.Flag != "" && inputSet(inputs[in.Name]):
			flags = append(flags, inputFlag(in, inputs[in.Name]))
		}
	}
	if takeOver == "" {
		return err
	}
	return fmt.Errorf("%s; run '"+brand.Command+" %s %s %s'", why, d.Name, strings.Join(append([]string{takeOver}, flags...), " "), ref.URL())
}

// actionRefusal is the why of d's [[action.refuse]] case, as written, that err
// ends with; "" for none.
func actionRefusal(err error, d dintent.Def) string {
	if err == nil || d.Action == nil {
		return ""
	}
	for _, r := range d.Action.Refuse {
		if r.Why.Src != "" && strings.HasSuffix(err.Error(), r.Why.Src) {
			return r.Why.Src
		}
	}
	return ""
}

// inputFlag is the command line words that set an input to v.
func inputFlag(in dintent.Input, v any) string {
	if s, ok := v.(string); ok && in.Type == "text" {
		return in.Flag + " " + shell.Quote(s)
	}
	return in.Flag
}

// intentUsage is the usage line of an intent's command, its target the units d serves.
func intentUsage(d dintent.Def) string {
	target := intentTarget(d)
	if target == "<url>" {
		target = "<https://github.com/<owner>/<repo>/pull/<n>>"
	}
	return "usage: " + brand.Command + " " + string(d.Name) + " " + target + intentFlags(d, surfaceCLI) + " [guidance...]"
}

// intentTarget is the target of d's command as a usage line names it: the
// units d serves, as their providers' codecs name them.
func intentTarget(d dintent.Def) string {
	ps := d.Providers
	if ps == nil {
		ps = []unit.ProviderID{d.ProviderOrDefault()}
	}
	if slices.Contains(ps, dintent.AnyProvider) {
		return pluginCodecs().Targets()
	}
	return pluginCodecs().Targets(ps...)
}

// intentFlags lists the flags of d a surface takes, as a usage line shows them.
func intentFlags(d dintent.Def, surface string) string {
	var b strings.Builder
	for _, in := range d.Inputs {
		if in.From != "user" || in.Hidden || in.Flag == "" || !slices.Contains(in.Surfaces, surface) {
			continue
		}
		if in.Type == "text" {
			fmt.Fprintf(&b, " [%s <%s>]", in.Flag, in.Name)
		} else {
			fmt.Fprintf(&b, " [%s]", in.Flag)
		}
	}
	if d.Intake && surface == surfaceCLI {
		fmt.Fprintf(&b, " [%s] [%s]", flagNoWait, flagNoBrief)
	}
	return b.String()
}

// brokenIntent is the error of a user intent's command when its manifest
// did not load.
func (e *Env) brokenIntent(name string) error {
	_, err := e.intentDef(name)
	return err
}
