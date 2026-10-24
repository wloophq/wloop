package slack

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

type wireEvent struct {
	wireMessage
	Type            string       `json:"type"`
	Channel         string       `json:"channel"`
	ChannelType     string       `json:"channel_type"`
	DeletedTS       string       `json:"deleted_ts"`
	Message         *wireMessage `json:"message"`
	PreviousMessage *wireMessage `json:"previous_message"`
	Reaction        string       `json:"reaction"`
	Item            struct {
		Type        string `json:"type"`
		Channel     string `json:"channel"`
		ChannelType string `json:"channel_type"`
		TS          string `json:"ts"`
	} `json:"item"`
}

// FromEvent maps every message, edit, delete, reply and reaction whatever its bot fields;
// ok is false only for non-message events and the hidden subtypes in HiddenSubtypes.
// team is the workspace the payload came from; a Delta does not carry it.
func FromEvent(team string, payload json.RawMessage, via Via, at time.Time) (Delta, bool, error) {
	var p struct {
		EventID string     `json:"event_id"`
		Event   *wireEvent `json:"event"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return Delta{}, false, fmt.Errorf("slack event: %w", err)
	}
	ev := p.Event
	if ev == nil {
		return Delta{}, false, nil
	}
	d := Delta{EventID: p.EventID, Via: via, ReceivedAt: at, Conversation: ev.Channel, ChannelType: ev.ChannelType}
	switch ev.Type {
	case "reaction_added", "reaction_removed":
		if ev.Item.Type != "message" || ev.Item.Channel == "" {
			return Delta{}, false, nil
		}
		d.Kind = Kind(ev.Type)
		d.Conversation, d.TS = ev.Item.Channel, ev.Item.TS
		d.ChannelType = cmp.Or(d.ChannelType, ev.Item.ChannelType)
		d.Reaction = &struct{ Name, User string }{ev.Reaction, ev.User}
		return d, true, nil
	case "message":
	default:
		return Delta{}, false, nil
	}
	if slices.Contains(HiddenSubtypes, ev.Subtype) {
		return Delta{}, false, nil
	}
	var m Message
	switch ev.Subtype {
	case "message_changed":
		if ev.Message == nil {
			return Delta{}, false, fmt.Errorf("slack event: message_changed without message")
		}
		d.Kind, m = KindEdit, ev.Message.message()
		if m.Deleted {
			d.Kind = KindDelete
		}
	case "message_deleted":
		d.Kind = KindDelete
		if ev.PreviousMessage != nil {
			m = ev.PreviousMessage.message()
		}
		m.TS, m.Deleted = cmp.Or(ev.DeletedTS, m.TS), true
	default:
		d.Kind, m = KindMessage, ev.message()
	}
	d.TS, d.ThreadTS, d.Message = m.TS, m.ThreadTS, &m
	return d, true, nil
}
