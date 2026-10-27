package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"

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
		return s.badArguments(slackDraftName, err)
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
		problems = append(problems, s.say(slackDraftName, "problem_kind", fields{"Kinds": strings.Join(draftKinds, ", ")}))
	case draft.Kind(args.Kind).Emoji() && args.Reaction == "":
		problems = append(problems, s.say(slackDraftName, "problem_reaction_required", nil))
	case !draft.Kind(args.Kind).Emoji() && strings.TrimSpace(args.Text) == "":
		problems = append(problems, s.say(slackDraftName, "problem_text_required", nil))
	}
	if len(problems) > 0 {
		return s.fixThese(slackDraftName, problems)
	}
	return s.draft(ctx, handle, args)
}

func (s *Server) draft(ctx context.Context, handle string, args DraftArgs) any {
	if s.Slack != nil && !s.Slack.LoggedIn() {
		return s.notLoggedIn(slackDraftName)
	}
	r, err := s.SlackDraft.Draft(ctx, handle, args)
	if errors.Is(err, send.ErrNotLoggedIn) {
		return s.notLoggedIn(slackDraftName)
	}
	if err != nil {
		return s.failed(slackDraftName, err)
	}
	name := "proposed"
	switch draft.State(r.State) {
	case draft.Approved:
		name = "approved"
	case draft.Discarded:
		name = "dry_run"
	}
	return s.answer(slackDraftName, name, fields{"ID": r.ID, "Unit": r.Unit, "Where": r.Conversation}, false)
}
