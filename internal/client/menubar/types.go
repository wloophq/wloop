package menubar

import (
	"context"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
)

type MenuItem struct {
	Title     string
	Enabled   bool
	Separator bool
	Checked   bool
	Action    func()
	Children  []MenuItem
}

type Tray interface {
	SetTitle(title string)
	SetMenu(items []MenuItem)
}

// Category is a notification's category, the first part of its id
// (api.NotificationID).
type Category = api.Category

type Notification struct {
	ID string
	// Title is the kind of notification, Subtitle its unit.
	Title    string
	Subtitle string
	Body     string
	Category Category
	Image    Image // "" posts no image
	Actions  []NotificationAction
}

// NotificationAction is a button of a notification; its ID is the
// daemon's action id, which ResponseFor reads back.
type NotificationAction struct {
	ID, Title string
	TextInput bool
}

// Image tells notifications apart at a glance.
type Image string

const (
	ImageRound   Image = "round"
	ImageHeld    Image = "held"
	ImageAsk     Image = "ask"
	ImageBlocked Image = "blocked"
)

type Action string

const (
	// ActionOpen opens the notification's entry in the cockpit.
	ActionOpen Action = "open"
	// ActionServed posts the action whose id is Response.Identifier.
	ActionServed Action = "served"
	// ActionAnswer answers the question with Response.Text, OptionID, Yes or
	// No.
	ActionAnswer Action = "answer"
	// ActionReply writes Response.Text as a reply on the entry's unit: to the
	// Outbox, or sent at once with Response.Send.
	ActionReply Action = "reply"
)

type Response struct {
	ID string
	// Identifier is the notification action the user picked, as the
	// notification center reported it.
	Identifier string
	Action     Action
	Text       string
	OptionID   string
	Yes, No    bool
	Send       bool
}

// ResponseFor is the response to the notification id when the user picks
// the action whose id the daemon gave; the text of a text input is added by
// the caller. System identifiers are not actions.
func ResponseFor(id, action string) (Response, bool) {
	r := Response{ID: id, Identifier: action}
	switch {
	case strings.HasPrefix(action, "answer:option:") && len(action) > len("answer:option:"):
		r.Action, r.OptionID = ActionAnswer, strings.TrimPrefix(action, "answer:option:")
	case action == "answer:yes":
		r.Action, r.Yes = ActionAnswer, true
	case action == "answer:no":
		r.Action, r.No = ActionAnswer, true
	case action == "answer:text":
		r.Action = ActionAnswer
	case action == api.AttentionReply:
		r.Action = ActionReply
	case action == api.AttentionSendReply:
		r.Action, r.Send = ActionReply, true
	case action == "" || strings.HasPrefix(action, "answer:") || strings.HasPrefix(action, "reply:") || strings.HasPrefix(action, "com.apple."):
		return Response{}, false
	default:
		r.Action = ActionServed
	}
	return r, true
}

// CategorySpec is a notification category as macOS registers it.
type CategorySpec struct {
	ID      Category
	Actions []NotificationAction
}

// Categories are the categories the notification center knows: the
// category of each notification posted with buttons and not withdrawn. It
// is not safe for concurrent use.
type Categories struct {
	m    map[Category][]NotificationAction
	live map[Category]map[string]bool
	of   map[string]Category
}

func NewCategories() *Categories {
	return &Categories{live: map[Category]map[string]bool{}, m: map[Category][]NotificationAction{}, of: map[string]Category{}}
}

// Post records the category of n with its actions; true when the set
// changed and must be registered again.
func (c *Categories) Post(n Notification) bool {
	if len(n.Actions) == 0 {
		return false
	}
	if c.live[n.Category] == nil {
		c.live[n.Category] = map[string]bool{}
	}
	c.live[n.Category][n.ID] = true
	c.of[n.ID] = n.Category
	if slices.Equal(c.m[n.Category], n.Actions) {
		return false
	}
	c.m[n.Category] = slices.Clone(n.Actions)
	return true
}

// Withdraw drops a category once no live notification uses it; true when
// the set changed.
func (c *Categories) Withdraw(ids []string) bool {
	changed := false
	for _, id := range ids {
		cat, ok := c.of[id]
		if !ok {
			continue
		}
		delete(c.of, id)
		delete(c.live[cat], id)
		if len(c.live[cat]) == 0 {
			delete(c.live, cat)
			delete(c.m, cat)
			changed = true
		}
	}
	return changed
}

func (c *Categories) List() []CategorySpec {
	out := make([]CategorySpec, 0, len(c.m))
	for id, actions := range c.m {
		out = append(out, CategorySpec{ID: id, Actions: slices.Clone(actions)})
	}
	slices.SortFunc(out, func(a, b CategorySpec) int { return strings.Compare(string(a.ID), string(b.ID)) })
	return out
}

type Permission string

const (
	PermissionAuthorized    Permission = "authorized"
	PermissionProvisional   Permission = "provisional"
	PermissionDenied        Permission = "denied"
	PermissionNotDetermined Permission = "not-determined"
)

// Notifier posts notifications and reports the user's answers. Authorize may
// block until the user answers the system dialog; Permission only reads the
// current setting.
type Notifier interface {
	Authorize(ctx context.Context) (Permission, error)
	Permission(ctx context.Context) (Permission, error)
	Post(n Notification) error
	Withdraw(ids []string)
	// WithdrawAll clears every notification delivered so far.
	WithdrawAll()
	OnResponse(fn func(Response))
}
