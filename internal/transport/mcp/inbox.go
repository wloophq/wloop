package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
)

// Inbox posts an agent's message to the user's Inbox.
type Inbox interface {
	Post(ctx context.Context, p messages.PostRequest) (message.Message, error)
}

const inboxPostName = "inbox_post"

func inboxPostTool() tool {
	option := object(map[string]*schema{
		"id":          str(20),
		"label":       str(record.LabelMax),
		"consequence": str(record.ConsequenceMax),
	}, "id", "label", "consequence")
	return tool{
		Name: inboxPostName,
		InputSchema: object(map[string]*schema{
			"text":      str(record.TextMax),
			"options":   {Type: "array", Items: option, MinItems: 2, MaxItems: 5},
			"recommend": str(record.RecommendMax),
			"confirm":   boolean(),
		}, "text"),
	}
}

type inboxPostArgs struct {
	Text      *string      `json:"text"`
	Options   []postOption `json:"options"`
	Recommend string       `json:"recommend"`
	Confirm   bool         `json:"confirm"`
}

type postOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Consequence string `json:"consequence"`
}

func (s *Server) inboxPost(ctx context.Context, handle string, arguments json.RawMessage) any {
	var a inboxPostArgs
	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		return s.badArguments(inboxPostName, err)
	}
	if a.Text == nil || strings.TrimSpace(*a.Text) == "" {
		return s.answer(inboxPostName, "text_required", nil, true)
	}
	req := messages.PostRequest{Session: handle, Text: *a.Text, Recommend: a.Recommend, Confirm: a.Confirm}
	for _, o := range a.Options {
		req.Options = append(req.Options, record.Option{ID: o.ID, Label: o.Label, Consequence: o.Consequence})
	}
	m, err := s.Inbox.Post(ctx, req)
	var problems record.Problems
	var capped messages.Capped
	switch {
	case errors.As(err, &problems):
		return s.fixThese(inboxPostName, problems)
	case errors.As(err, &capped):
		return s.answer(inboxPostName, "capped", fields{"Error": capped.Error()}, true)
	case errors.Is(err, messages.ErrNoStartedTurn):
		return s.answer(inboxPostName, "not_in_turn", fields{"Error": err.Error()}, true)
	case err != nil:
		return s.answer(inboxPostName, "post_failed", fields{"Error": err.Error()}, true)
	}
	return s.answer(inboxPostName, "posted", fields{"ID": int64(m.ID)}, false)
}
