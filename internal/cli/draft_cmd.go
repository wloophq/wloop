package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/transport/mcp"
)

const draftUsage = "usage: draft propose --kind <kind> [--unit <provider>/<key>] [--target <t>] [--text <t> | --text-file <path>] [--payload <json> | --payload-file <path>] [--basis <b>] [--hold] [--why <w>] [--item <id>]... | " +
	"draft read <id> | draft update <id> --rev <rev> [--text <t> | --text-file <path>] [--payload <json> | --payload-file <path>] [--basis <b>] | draft withdraw <id> --rev <rev> [--reason <r>]"

// draftAgent is the part of the agent API the draft command calls.
type draftAgent interface {
	ProposeDraft(ctx context.Context, handle string, req api.AgentDraftProposal) (api.AgentDraft, error)
	ReadDraft(ctx context.Context, handle string, id int64) (api.AgentDraft, error)
	UpdateDraft(ctx context.Context, handle string, req api.AgentDraftUpdate) (api.AgentDraft, error)
	WithdrawDraft(ctx context.Context, handle string, req api.AgentDraftWithdrawal) (api.AgentDraft, error)
}

// runDraft is an agent session's calls on its drafts, as the MCP draft tools make them; the daemon reads
// the session from the session's environment only.
func runDraft(ctx context.Context, e *Env, args []string) int {
	return draftCommand(ctx, e, e.agentClient(), e.sessionHandle(), args)
}

func draftCommand(ctx context.Context, e *Env, c draftAgent, handle string, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(e.Err, draftUsage)
		return 2
	}
	verb := args[0]
	got, code, err := draftCall(ctx, c, handle, verb, args[1:])
	if code != 0 {
		if err != nil {
			fmt.Fprintf(e.Err, "%s draft %s: %v\n", brand.Command, verb, err)
		}
		fmt.Fprintln(e.Err, draftUsage)
		return code
	}
	if errors.Is(err, client.ErrDown) {
		fmt.Fprintf(e.Err, brand.Name+"'s daemon is not running; start it with `%s`, then run this command again\n", client.Kickstart())
		return 1
	}
	if err != nil {
		fmt.Fprintf(e.Err, "%s draft %s: %s\n", brand.Command, verb, strings.TrimPrefix(err.Error(), "daemon: "))
		return 1
	}
	fmt.Fprintln(e.Out, mcp.DraftJSON(got))
	return 0
}

// draftCall runs verb; code is 2 when the arguments are wrong.
func draftCall(ctx context.Context, c draftAgent, handle, verb string, args []string) (api.AgentDraft, int, error) {
	id, args := draftIDArg(args)
	fs := flag.NewFlagSet("draft "+verb, flag.ContinueOnError)
	fs.SetOutput(new(strings.Builder))
	rev := fs.Int64("rev", 0, "the rev you read")
	var text, textFile, payload, payloadFile, basis optString
	fs.Var(&text, "text", "the text")
	fs.Var(&textFile, "text-file", "a file holding the text")
	fs.Var(&payload, "payload", "the payload, a JSON object")
	fs.Var(&payloadFile, "payload-file", "a file holding the payload")
	fs.Var(&basis, "basis", "what you read the unit at")
	reason := fs.String("reason", "", "why it is withdrawn")
	kind := fs.String("kind", "", "the draft's kind")
	unitArg := fs.String("unit", "", "the unit, <provider>/<key>")
	target := fs.String("target", "", "the target")
	hold := fs.Bool("hold", false, "keep it waiting for the user")
	why := fs.String("why", "", "why it is proposed")
	var items listFlag
	fs.Var(&items, "item", "an item it answers")
	if err := fs.Parse(args); err != nil {
		return api.AgentDraft{}, 2, err
	}
	if fs.NArg() > 0 {
		return api.AgentDraft{}, 2, fmt.Errorf("unexpected arguments %v", fs.Args())
	}
	t, err := text.orFile(textFile)
	if err != nil {
		return api.AgentDraft{}, 2, err
	}
	p, err := payload.orFile(payloadFile)
	if err != nil {
		return api.AgentDraft{}, 2, err
	}
	var raw json.RawMessage
	if p != nil {
		if !json.Valid([]byte(*p)) {
			return api.AgentDraft{}, 2, errors.New("--payload is not JSON")
		}
		raw = json.RawMessage(*p)
	}
	switch verb {
	case "propose":
		if id != 0 || *kind == "" {
			return api.AgentDraft{}, 2, errors.New("propose takes --kind and no id")
		}
		req := api.AgentDraftProposal{Unit: *unitArg, Kind: *kind, Target: *target, Payload: raw, Basis: basis.value(), Hold: *hold, Why: *why, Items: items}
		if t != nil {
			req.Text = *t
		}
		got, err := c.ProposeDraft(ctx, handle, req)
		return got, 0, err
	case "read":
		if id <= 0 {
			return api.AgentDraft{}, 2, errors.New("read takes a draft's id")
		}
		got, err := c.ReadDraft(ctx, handle, id)
		return got, 0, err
	case "update":
		if id <= 0 || *rev <= 0 {
			return api.AgentDraft{}, 2, errors.New("update takes a draft's id and --rev")
		}
		got, err := c.UpdateDraft(ctx, handle, api.AgentDraftUpdate{ID: id, Rev: *rev, Text: t, Payload: raw, Basis: basis.ptr()})
		return got, 0, err
	case "withdraw":
		if id <= 0 || *rev <= 0 {
			return api.AgentDraft{}, 2, errors.New("withdraw takes a draft's id and --rev")
		}
		got, err := c.WithdrawDraft(ctx, handle, api.AgentDraftWithdrawal{ID: id, Rev: *rev, Reason: *reason})
		return got, 0, err
	}
	return api.AgentDraft{}, 2, fmt.Errorf("no verb %s", verb)
}

// draftIDArg takes the draft's id when it comes first; 0 when none, -1 when it is not a number.
func draftIDArg(args []string) (int64, []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return 0, args
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return -1, args[1:]
	}
	return id, args[1:]
}

// optString is a flag that tells unset from empty.
type optString struct{ v *string }

func (o *optString) String() string {
	if o.v == nil {
		return ""
	}
	return *o.v
}

func (o *optString) Set(s string) error {
	o.v = &s
	return nil
}

func (o optString) ptr() *string { return o.v }

func (o optString) value() string { return o.String() }

// orFile is o, or the content of the file f names; both set is an error.
func (o optString) orFile(f optString) (*string, error) {
	switch {
	case o.v != nil && f.v != nil:
		return nil, errors.New("give the value or its file, not both")
	case f.v != nil:
		raw, err := os.ReadFile(*f.v)
		if err != nil {
			return nil, err
		}
		s := string(raw)
		return &s, nil
	}
	return o.v, nil
}
