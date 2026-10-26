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
var draftKinds = func() []string {
	out := make([]string, len(draft.ConversationKinds))
	for i, k := range draft.ConversationKinds {
		out[i] = string(k)
	}
	return out
}()

func slackDraftTool() tool {
	return tool{
		Name: slackDraftName,
		InputSchema: object(map[string]*schema{
			"text":     str(0),
			"kind":     enum(draftKinds...),
			"target":   str(0),
			"items":    {Type: "array", Items: str(0)},
			"reaction": str(0),
			"effects":  {Type: "array", Items: str(0)},
		}),
	}
}

type slackDraftArgs struct {
	Text     *string  `json:"text"`
	Kind     string   `json:"kind"`
	Target   string   `json:"target"`
	Items    []string `json:"items"`
	Reaction string   `json:"reaction"`
	Effects  []string `json:"effects"`
}

func (s *Server) slackDraft(ctx context.Context, handle string, arguments json.RawMessage) any {
	var a slackDraftArgs
	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		reason := "an object with text, and kind, target, items, reaction and effects when needed"
		if err != nil {
			reason += " (" + err.Error() + ")"
		}
		return text("Nothing was drafted: the arguments are not "+reason+".", true)
	}
	args := DraftArgs{Kind: strings.TrimSpace(a.Kind), Target: strings.TrimSpace(a.Target), Items: a.Items, Reaction: strings.TrimSpace(a.Reaction),
		Effects: a.Effects}
	if a.Text != nil {
		args.Text = *a.Text
	}
	if args.Kind == "" {
		args.Kind = draftKinds[0]
	}
	var problems []string
	switch {
	case !slices.Contains(draftKinds, args.Kind):
		problems = append(problems, "kind: one of "+strings.Join(draftKinds, ", "))
	case draft.Kind(args.Kind).Emoji() && args.Reaction == "":
		problems = append(problems, "reaction: required for a react draft")
	case !draft.Kind(args.Kind).Emoji() && strings.TrimSpace(args.Text) == "":
		problems = append(problems, "text: required, not empty")
	}
	if len(problems) > 0 {
		return text("Nothing was drafted; fix these and call "+slackDraftName+" again:\n"+strings.Join(problems, "\n"), true)
	}
	return s.draft(ctx, handle, args)
}

func (s *Server) draft(ctx context.Context, handle string, args DraftArgs) any {
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
		return fmt.Sprintf("Draft %d on %s is a dry run: its approval stores drafts and never sends them, so it will not be posted to %s.", r.ID, r.Unit, where)
	}
	return fmt.Sprintf("Proposed draft %d on %s: it will be posted to %s once the user approves it in the %s Outbox; nothing was posted.", r.ID, r.Unit, where, brand.Name)
}
