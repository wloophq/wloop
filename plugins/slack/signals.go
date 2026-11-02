package slack

import (
	"slices"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	slackrows "github.com/wloophq/wloop/plugins/slack/rows"
	"github.com/wloophq/wloop/plugins/slack/store"
)

// The link signals the Slack plugin's manifest declares.
const (
	signalAskedReacted = "asked_reacted"
	signalRepliedLast  = "replied_last"
)

// repliedAfter is how long the viewer's reply stays a unit's newest person's message before replied_last holds.
const repliedAfter = 3 * 24 * time.Hour

// checkMarks are the reactions that read as "done".
var checkMarks = []string{"white_check_mark", "heavy_check_mark", "ballot_box_with_check"}

// unitSignals are in's link signals at now, and when the next of them changes with time alone: asked_reacted,
// every open item has a check mark from whoever asked it or whom it waits on, other than the viewer, on a
// message posted after its ask; replied_last, the viewer posted the newest person's message, after every open
// item's ask, at least quietFor ago, and every open item waits on the viewer or got a message from someone else
// after its ask.
func unitSignals(in *slackrows.Input, quietFor time.Duration, now time.Time) ([]src.Signal, time.Time) {
	open := slices.DeleteFunc(slices.Clone(in.Items()), func(it items.Item) bool { return it.State != items.Open })
	asked := src.Signal{Name: signalAskedReacted}
	replied := src.Signal{Name: signalRepliedLast}
	if len(open) == 0 {
		return []src.Signal{asked, replied}, time.Time{}
	}
	var newestAsk time.Time
	asked.On = true
	answered := in.Viewer() != ""
	for _, it := range open {
		newestAsk = later(newestAsk, it.Origin.At)
		answered = answered && (waitsOnViewer(in, it) || othersSpokeAfter(in, it.Origin.At))
		at, ok := checkedAfter(in, it)
		if !ok {
			asked.On = false
			continue
		}
		asked.Since = later(asked.Since, at)
	}
	if !asked.On {
		asked.Since = time.Time{}
	}
	var next time.Time
	if m, ok := newestHuman(in); ok && answered && m.User == in.Viewer() {
		at := dslack.TSTime(m.TS)
		if at.After(newestAsk) {
			quiet := at.Add(quietFor)
			if now.Before(quiet) {
				next = quiet
			} else {
				replied.On, replied.Since = true, at
			}
		}
	}
	return []src.Signal{asked, replied}, next
}

// checkedAfter is when the message carrying the first check mark on it by whoever asked it or whom it waits
// on, other than the viewer, was posted after its ask; false when none.
func checkedAfter(in *slackrows.Input, it items.Item) (time.Time, bool) {
	party := []string{it.Requester.Key}
	for _, p := range it.WaitingOn {
		party = append(party, p.Key)
	}
	for _, m := range in.Messages() {
		at := dslack.TSTime(m.TS)
		if m.Deleted || !at.After(it.Origin.At) {
			continue
		}
		for _, r := range m.Reactions {
			name, _, _ := strings.Cut(r.Name, "::")
			if !slices.Contains(checkMarks, name) {
				continue
			}
			if slices.ContainsFunc(r.Users, func(u string) bool { return u != in.Viewer() && slices.Contains(party, "slack:"+u) }) {
				return at, true
			}
		}
	}
	return time.Time{}, false
}

// newestHuman is in's newest person's message.
func newestHuman(in *slackrows.Input) (store.MessageRow, bool) {
	msgs := in.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if in.Human(msgs[i]) {
			return msgs[i], true
		}
	}
	return store.MessageRow{}, false
}

func waitsOnViewer(in *slackrows.Input, it items.Item) bool {
	return slices.ContainsFunc(it.WaitingOn, func(p items.Person) bool { return p.Key == "slack:"+in.Viewer() })
}

// othersSpokeAfter says whether a person other than the viewer posted in in after at.
func othersSpokeAfter(in *slackrows.Input, at time.Time) bool {
	return slices.ContainsFunc(in.Messages(), func(m store.MessageRow) bool {
		return in.Human(m) && m.User != in.Viewer() && dslack.TSTime(m.TS).After(at)
	})
}
