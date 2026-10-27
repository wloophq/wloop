// Package turncli is the agent's command line for its turn: `turn end`
// records how the turn ended, `turn answer` reads the user's answer.
package turncli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/harness"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	endUsage    = "usage: turn end <pr-url>|<provider>:<key> --turn <id> --file <path>"
	answerUsage = "usage: turn answer <pr-url>|<provider>:<key> [--message <id>]"
)

// Agent is the part of AgentAPI the agent's commands call.
type Agent interface {
	EndTurn(ctx context.Context, handle string, r api.TurnEndRequest) (api.TurnEnded, error)
	TurnAnswer(ctx context.Context, handle, arg string, message int64) (api.TurnText, error)
}

// End stores the record in the file for the turn it names, and deletes the
// file once stored. A record the command line can tell is malformed never
// reaches the daemon.
func End(ctx context.Context, env func(string) string, args []string, out, errw io.Writer, c Agent, h harness.Set) int {
	url, args := positional(args)
	fs := flag.NewFlagSet("turn end", flag.ContinueOnError)
	fs.SetOutput(errw)
	id := fs.String("turn", "", "the id on the first line of the turn's instructions")
	file := fs.String("file", "", "the JSON file that holds the record")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if url == "" && fs.NArg() == 1 {
		url = fs.Arg(0)
	} else if fs.NArg() > 0 {
		url = ""
	}
	if url == "" || *id == "" || *file == "" {
		fmt.Fprintln(errw, endUsage)
		return 2
	}
	arg, label, err := unitArg(url)
	if err != nil {
		fmt.Fprintln(errw, brand.Command+" turn end:", err)
		return 1
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintln(errw, brand.Command+" turn end:", err)
		return 1
	}
	fi, err := os.Stat(*file)
	if err != nil {
		fmt.Fprintln(errw, brand.Command+" turn end:", err)
		return 1
	}
	rec, err := record.Parse(raw)
	if err == nil {
		err = rec.ValidateShape()
	}
	if err != nil {
		fmt.Fprintf(errw, "the record in %s was refused; fix every problem and run this command again:\n%s\n", *file, err)
		return 1
	}
	got, err := c.EndTurn(ctx, handle(env, h), api.TurnEndRequest{
		Turn: *id, PR: arg, Record: raw, AgentSession: agentSession(env, h), WrittenAt: fi.ModTime(),
	})
	if errors.Is(err, client.ErrDown) {
		fmt.Fprintf(errw, brand.Name+"'s daemon is not running, so this record was not stored; start it with `%s`, then run this command again\n", client.Kickstart())
		return 1
	}
	if err != nil {
		printError(errw, brand.Command+" turn end", err)
		return 1
	}
	if err := os.Remove(*file); err != nil {
		fmt.Fprintln(errw, brand.Command+" turn end: the record is stored, but", err)
	}
	if got.Note != "" {
		fmt.Fprintf(out, "Recorded %s for %s, but %s. End your turn.\n", got.State, label, got.Note)
		return 0
	}
	fmt.Fprintf(out, "Recorded %s for %s. End your turn.\n", got.State, label)
	return 0
}

// Answer prints the newest answer the user gave to the caller's turn.
func Answer(ctx context.Context, env func(string) string, args []string, out, errw io.Writer, c Agent, h harness.Set) int {
	var message int64
	switch {
	case len(args) == 3 && args[1] == "--message":
		n, err := strconv.ParseInt(args[2], 10, 64)
		if err != nil || n <= 0 {
			fmt.Fprintln(errw, answerUsage)
			return 2
		}
		message = n
	case len(args) != 1:
		fmt.Fprintln(errw, answerUsage)
		return 2
	}
	arg, _, err := unitArg(args[0])
	if err != nil {
		fmt.Fprintln(errw, brand.Command+" turn answer:", err)
		return 1
	}
	got, err := c.TurnAnswer(ctx, handle(env, h), arg, message)
	if errors.Is(err, client.ErrDown) {
		fmt.Fprintf(errw, brand.Name+"'s daemon is not running; start it with `%s`, then run this command again\n", client.Kickstart())
		return 1
	}
	if err != nil {
		printError(errw, brand.Command+" turn answer", err)
		return 1
	}
	fmt.Fprintln(out, strings.TrimRight(got.Text, "\n"))
	return 0
}

// unitArg reads the unit a turn command names: a pull request URL, sent as
// its canonical URL, or "<provider>:<key>", sent as given; label is how the
// command's output names it.
func unitArg(raw string) (arg, label string, err error) {
	ref, err := pr.ParseURL(raw)
	if err == nil {
		return ref.URL(), ref.String(), nil
	}
	if p, key, ok := strings.Cut(raw, ":"); ok && unit.ValidProvider(p) && key != "" && strings.IndexByte(" \t\n\f\r", key[0]) < 0 && !strings.HasPrefix(raw, "http") {
		return raw, raw, nil
	}
	return "", "", err
}

func handle(env func(string) string, h harness.Set) string {
	if s := h.Session(env); s != nil {
		return s.Handle(env)
	}
	return ""
}

func agentSession(env func(string) string, h harness.Set) string {
	if p := h.Detect(env); p != nil {
		return p.SessionID(env)
	}
	return ""
}

// positional takes the pull request URL when it comes before the flags.
func positional(args []string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

func printError(errw io.Writer, prefix string, err error) {
	msg := strings.TrimPrefix(err.Error(), "daemon: ")
	fmt.Fprintf(errw, "%s: %s\n", prefix, msg)
	var detailed interface{ Detail() string }
	if errors.As(err, &detailed) && detailed.Detail() != "" {
		fmt.Fprintln(errw, detailed.Detail())
	}
}
