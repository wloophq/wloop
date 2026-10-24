package slack

import (
	"context"
	"net/url"
)

type Reaction struct {
	Name  string   `json:"name"`
	Users []string `json:"users"`
	Count int      `json:"count"`
}

func (c *Client) AddReaction(ctx context.Context, channel, ts, name string) error {
	return c.Call(ctx, "reactions.add", reactionArgs(channel, ts, name), nil)
}

func (c *Client) RemoveReaction(ctx context.Context, channel, ts, name string) error {
	return c.Call(ctx, "reactions.remove", reactionArgs(channel, ts, name), nil)
}

// Reactions reads a message's reactions with every user (reactions.get, full=true).
func (c *Client) Reactions(ctx context.Context, channel, ts string) ([]Reaction, error) {
	var out struct {
		Message struct {
			Reactions []Reaction `json:"reactions"`
		} `json:"message"`
	}
	err := c.Call(ctx, "reactions.get", url.Values{"channel": {channel}, "timestamp": {ts}, "full": {"true"}}, &out)
	return out.Message.Reactions, err
}

func reactionArgs(channel, ts, name string) url.Values {
	return url.Values{"channel": {channel}, "timestamp": {ts}, "name": {name}}
}
