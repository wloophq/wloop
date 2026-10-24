package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/slack/send"
)

const slackDraftName = "slack_draft"

// draftKinds are the kinds slack_draft offers; a react posts no message.
var draftKinds = []string{string(draft.Reply), string(draft.Nudge), string(draft.Close), string(draft.AskOwner), string(draft.React)}

func slackDraftTool() tool {
	return tool{
		Name: slackDraftName,
		Description: fmt.Sprintf("Propose a Slack message, or a reaction, on this session's Slack unit, to be posted as the user once they approve it in the %s Outbox. "+
			"It posts nothing itself. Name people as @{slack:<user id>}.", brand.Name),
		InputSchema: object(map[string]*schema{
			"text":     str(0, "the message, in Markdown; empty for a reaction"),
			"kind":     enum("what the draft is; defaults to reply", draftKinds...),
			"target":   str(0, "where it posts: empty for the unit's thread or direct message, else a message permalink, #channel, channel id or user id"),
			"items":    {Type: "array", Items: str(0, "an item id of the unit"), Description: "the unit's items the draft answers"},
			"reaction": str(0, "the emoji name of a react draft, without colons"),
		}),
	}
}

type slackDraftArgs struct {
	Text     *string  `json:"text"`
	Kind     string   `json:"kind"`
	Target   string   `json:"target"`
	Items    []string `json:"items"`
	Reaction string   `json:"reaction"`
}

func (s *Server) slackDraft(ctx context.Context, handle string, arguments json.RawMessage) any {
	var a slackDraftArgs
	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		reason := "an object with text, and kind, target, items and reaction when needed"
		if err != nil {
			reason += " (" + err.Error() + ")"
		}
		return text("Nothing was drafted: the arguments are not "+reason+".", true)
	}
	args := DraftArgs{Kind: strings.TrimSpace(a.Kind), Target: strings.TrimSpace(a.Target), Items: a.Items, Reaction: strings.TrimSpace(a.Reaction)}
	if a.Text != nil {
		args.Text = *a.Text
	}
	if args.Kind == "" {
		args.Kind = string(draft.Reply)
	}
	var problems []string
	switch {
	case !slices.Contains(draftKinds, args.Kind):
		problems = append(problems, "kind: one of "+strings.Join(draftKinds, ", "))
	case args.Kind == string(draft.React) && args.Reaction == "":
		problems = append(problems, "reaction: required for a react draft")
	case args.Kind != string(draft.React) && strings.TrimSpace(args.Text) == "":
		problems = append(problems, "text: required, not empty")
	}
	if len(problems) > 0 {
		return text("Nothing was drafted; fix these and call "+slackDraftName+" again:\n"+strings.Join(problems, "\n"), true)
	}
	if s.Slack != nil && !s.Slack.LoggedIn() {
		return text(notLoggedIn(), true)
	}
	r, err := s.SlackDraft.Draft(ctx, handle, args)
	if errors.Is(err, send.ErrNotLoggedIn) {
		return text(notLoggedIn(), true)
	}
	if err != nil {
		return text("Nothing was drafted: "+redactTokens(err.Error()), true)
	}
	return text(draftedText(r), false)
}

func draftedText(r DraftResult) string {
	where := r.Conversation
	if where == "" {
		where = "its conversation"
	}
	switch draft.State(r.State) {
	case draft.Approved:
		return fmt.Sprintf("Draft %d on %s is approved by its policy and will be posted to %s; nothing was posted yet.", r.ID, r.Unit, where)
	case draft.Discarded:
		return fmt.Sprintf("Draft %d on %s was recorded only: its policy observes drafts, so it will not be posted to %s.", r.ID, r.Unit, where)
	}
	return fmt.Sprintf("Proposed draft %d on %s: it will be posted to %s once the user approves it in the %s Outbox; nothing was posted.", r.ID, r.Unit, where, brand.Name)
}
