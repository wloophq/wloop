// Package turncli is the agent's command line for its turn: `turn end`
// records how the turn ended, `turn answer` reads the user's answer.
package turncli

import (
	"context"
	"encoding/json"
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
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	endUsage    = "usage: turn end <pr-url>|<provider>:<key> --turn <id> --file <path> [--draft <id>:<rev>]"
	answerUsage = "usage: turn answer <pr-url>|<provider>:<key> [--gate <id>]"
)

// Agent is the part of AgentAPI the agent's commands call.
type Agent interface {
	EndTurn(ctx context.Context, handle string, r api.TurnEndRequest) (api.TurnEnded, error)
	TurnAnswer(ctx context.Context, handle, arg string, gate int64) (api.TurnText, error)
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
	draftRef := fs.String("draft", "", "the draft the turn ended on, <id>:<rev>; it sets the record's outcome.draft")
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
	if *draftRef != "" {
		if raw, err = withOutcomeDraft(raw, *draftRef); err != nil {
			fmt.Fprintln(errw, brand.Command+" turn end:", err)
			return 2
		}
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

// Answer prints the user's answers to the caller's gates, as task_read words them.
func Answer(ctx context.Context, env func(string) string, args []string, out, errw io.Writer, c Agent, h harness.Set) int {
	raw, args := positional(args)
	fs := flag.NewFlagSet("turn answer", flag.ContinueOnError)
	fs.SetOutput(errw)
	gate := fs.Int64("gate", 0, "the id of the gate whose answers to read; every unread answer without it")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if raw == "" && fs.NArg() == 1 {
		raw = fs.Arg(0)
	} else if fs.NArg() > 0 {
		raw = ""
	}
	if raw == "" || *gate < 0 {
		fmt.Fprintln(errw, answerUsage)
		return 2
	}
	arg, _, err := unitArg(raw)
	if err != nil {
		fmt.Fprintln(errw, brand.Command+" turn answer:", err)
		return 1
	}
	got, err := c.TurnAnswer(ctx, handle(env, h), arg, *gate)
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

// unitArg reads the unit a turn command names: a unit's web address, sent
// as its canonical address, or "<provider>:<key>", sent as given; label is
// how the command's output names it.
func unitArg(raw string) (arg, label string, err error) {
	if u, ok := unit.ParseAddress(strings.TrimSpace(raw)); ok {
		return u.Address(), u.Label(), nil
	}
	if p, key, ok := strings.Cut(raw, ":"); ok && unit.ValidProvider(p) && key != "" && strings.IndexByte(" \t\n\f\r", key[0]) < 0 && !strings.HasPrefix(raw, "http") {
		return raw, raw, nil
	}
	return "", "", fmt.Errorf("not a unit's address or <provider>:<key>: %s", raw)
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

// withOutcomeDraft is the record raw with outcome.draft set to ref, "<id>:<rev>"; the record's other
// fields stay as written.
func withOutcomeDraft(raw []byte, ref string) ([]byte, error) {
	id, rev, ok := strings.Cut(ref, ":")
	d := record.DraftRef{}
	var err error
	if ok {
		if d.ID, err = strconv.ParseInt(id, 10, 64); err == nil {
			d.Rev, err = strconv.ParseInt(rev, 10, 64)
		}
	}
	if !ok || err != nil || d.ID <= 0 || d.Rev <= 0 {
		return nil, fmt.Errorf("--draft %s is not <id>:<rev>", ref)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("the record is not a JSON object: %w", err)
	}
	outcome := map[string]json.RawMessage{}
	if o, found := top["outcome"]; found && string(o) != "null" {
		if err := json.Unmarshal(o, &outcome); err != nil {
			return nil, fmt.Errorf("the record's outcome is not a JSON object: %w", err)
		}
	}
	if outcome["draft"], err = json.Marshal(d); err != nil {
		return nil, err
	}
	if top["outcome"], err = json.Marshal(outcome); err != nil {
		return nil, err
	}
	return json.Marshal(top)
}
