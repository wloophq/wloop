package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
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
		"id":          str(20, "lowercase letters, digits and dashes"),
		"label":       str(record.LabelMax, ""),
		"consequence": str(record.ConsequenceMax, "what choosing this option does"),
	}, "id", "label", "consequence")
	return tool{
		Name: inboxPostName,
		Description: fmt.Sprintf("Post a message to the user's %s Inbox during your turn, without ending it: a question or a note the user answers later. "+
			"At most %d of your messages wait at once. It posts only; it cannot move, list or close Inbox entries.", brand.Name, messages.PostsPerTurn),
		InputSchema: object(map[string]*schema{
			"text":      str(record.TextMax, "the message"),
			"options":   {Type: "array", Items: option, MinItems: 2, MaxItems: 5, Description: "the choices, when the user picks one"},
			"recommend": str(record.RecommendMax, "an option id, or yes or no with confirm"),
			"confirm":   boolean("a yes or no question"),
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
		reason := "an object with text, and options, recommend and confirm when needed; " + inboxPostName + " posts a message and cannot move, list or close entries"
		if err != nil {
			reason += " (" + err.Error() + ")"
		}
		return text("Nothing was posted: the arguments are not "+reason+".", true)
	}
	if a.Text == nil || strings.TrimSpace(*a.Text) == "" {
		return text("Nothing was posted: text is required.", true)
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
		return text("Nothing was posted; fix these and call "+inboxPostName+" again:\n"+problems.Error(), true)
	case errors.As(err, &capped):
		return text("Nothing was posted: "+capped.Error()+".", true)
	case errors.Is(err, messages.ErrNoStartedTurn):
		return text("Nothing was posted: "+err.Error()+"; "+inboxPostName+" works only during a started "+brand.Name+" turn.", true)
	case err != nil:
		return text(brand.Name+" could not post the message: "+err.Error(), true)
	}
	return text(fmt.Sprintf("Posted to the Inbox as message %d; the user's answer is typed into this session when it is idle.", m.ID), false)
}
