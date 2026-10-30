package api

import (
	"strconv"
	"strings"
)

// Category is the first part of a notification id: Q<record-id>, INBOX, OUTBOX, HELD, DIGEST or NOTE.
type Category string

const (
	CategoryInbox  Category = "INBOX"
	CategoryOutbox Category = "OUTBOX"
	// CategoryHeld's token is the local day, so it is posted once a day.
	CategoryHeld Category = "HELD"
	// CategoryDigest's task is "-" and its token the day.
	CategoryDigest Category = "DIGEST"
	CategoryNote   Category = "NOTE"
)

// QuestionCategory is the category of the question of turn record record.
func QuestionCategory(record int64) Category {
	return Category("Q" + strconv.FormatInt(record, 10))
}

// Record is the turn record a question category names; ok is false for any other category.
func (c Category) Record() (int64, bool) {
	digits, ok := strings.CutPrefix(string(c), "Q")
	if !ok || digits == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != digits {
		return 0, false
	}
	return n, true
}

func (c Category) known() bool {
	switch c {
	case CategoryInbox, CategoryOutbox, CategoryHeld, CategoryDigest, CategoryNote:
		return true
	}
	_, ok := c.Record()
	return ok
}

// NotificationID is "<category>|<task-id>|<token>".
func NotificationID(cat Category, task string, token string) string {
	return string(cat) + "|" + task + "|" + token
}

// ParseNotificationID reads an id NotificationID wrote.
func ParseNotificationID(id string) (cat Category, task string, ok bool) {
	p := strings.Split(id, "|")
	if len(p) != 3 || p[1] == "" || p[2] == "" || !Category(p[0]).known() {
		return "", "", false
	}
	return Category(p[0]), p[1], true
}

type Digest struct {
	Day    string   `json:"day"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Counts Counters `json:"counts"`
	Tasks  []string `json:"tasks"`
}
