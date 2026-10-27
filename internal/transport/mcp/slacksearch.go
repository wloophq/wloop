package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/slack/send"
)

const (
	slackSearchName = "slack_search"
	searchLimit     = 20
	searchLimitMax  = 100
)

// SlackSearcher searches the Slack messages the user can read.
type SlackSearcher interface {
	Search(ctx context.Context, a SearchArgs) (string, error)
}

// SearchArgs are slack_search's arguments: the query and the filters set.
type SearchArgs struct {
	Query, Channel, From, After, Before string
	Limit                               int
}

func slackSearchTool() tool {
	one := 1
	return tool{Name: slackSearchName, InputSchema: object(map[string]*schema{
		"query":   str(0),
		"channel": str(0),
		"from":    str(0),
		"after":   str(10),
		"before":  str(10),
		"limit":   {Type: "integer", Minimum: &one},
	}, "query")}
}

type slackSearchArgs struct {
	Query   *string         `json:"query"`
	Channel string          `json:"channel"`
	From    string          `json:"from"`
	After   string          `json:"after"`
	Before  string          `json:"before"`
	Limit   json.RawMessage `json:"limit"`
}

func (s *Server) slackSearch(ctx context.Context, _ string, arguments json.RawMessage) any {
	var a slackSearchArgs
	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		reason := "an object with query, and channel, from, after, before and limit when needed"
		if err != nil {
			reason += "; " + err.Error()
		}
		return text("The arguments are not "+reason+".", true)
	}
	args := SearchArgs{Channel: strings.TrimSpace(a.Channel), From: strings.TrimSpace(a.From), After: strings.TrimSpace(a.After),
		Before: strings.TrimSpace(a.Before), Limit: searchLimit}
	var problems []string
	if a.Query != nil {
		args.Query = strings.TrimSpace(*a.Query)
	}
	if args.Query == "" {
		problems = append(problems, "query: required, not empty")
	}
	after, afterOK := searchDate(args.After)
	before, beforeOK := searchDate(args.Before)
	if !afterOK {
		problems = append(problems, "after: a date, YYYY-MM-DD")
	}
	if !beforeOK {
		problems = append(problems, "before: a date, YYYY-MM-DD")
	}
	if afterOK && beforeOK && !after.IsZero() && !before.IsZero() && !before.After(after) {
		problems = append(problems, "before: a date after after")
	}
	if a.Limit != nil {
		var n int
		if err := json.Unmarshal(a.Limit, &n); err != nil || n < 1 || n > searchLimitMax {
			problems = append(problems, "limit: an integer from 1 to 100")
		}
		args.Limit = n
	}
	if len(problems) > 0 {
		return text("Nothing was searched; fix these and call "+slackSearchName+" again:\n"+strings.Join(problems, "\n"), true)
	}
	if s.Slack != nil && !s.Slack.LoggedIn() {
		return text(notLoggedIn(), true)
	}
	out, err := s.SlackSearch.Search(ctx, args)
	if errors.Is(err, send.ErrNotLoggedIn) {
		return text(notLoggedIn(), true)
	}
	if err != nil {
		return text("Slack search failed: "+redactTokens(err.Error()), true)
	}
	return text(redactTokens(out), false)
}

// searchDate is s as a date; ok is false when s is set and not YYYY-MM-DD.
func searchDate(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, true
	}
	t, err := time.Parse(time.DateOnly, s)
	return t, err == nil
}
