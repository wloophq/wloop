package prrow

import (
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/situation"
)

// The categories the menu bar posts under; a question's is "Q<record-id>".
const (
	categoryRound   = "ROUND"
	categoryHeld    = "HELD"
	categoryAsk     = "ASK"
	categoryBlocked = "BLOCKED"
	// categoryWait is an agent waiting with no question the loop holds, such
	// as a permission prompt: Open is all there is to do.
	categoryWait = "WAIT"
)

var actionReply = api.AttentionAction{ID: "answer:text", Title: "Reply", TextInput: true}

// questionOf is the question view of a turn that waits on the user, from
// its last record; ok is false for any other turn.
func questionOf(t *turn.Turn, last *turn.Record) (*api.QuestionView, bool) {
	if t.State != turn.Asked && !t.State.Stopped() {
		return nil, false
	}
	q := &api.QuestionView{State: string(t.State)}
	if last == nil {
		return q, true
	}
	q.RecordID = last.ID
	if rq := last.Record.Question; rq != nil {
		q.Kind, q.Text, q.Options, q.Recommend, q.Publishes = rq.Kind, rq.Text, slices.Clone(rq.Options), rq.Recommend, rq.Publishes
	}
	if o := last.Record.Outcome; o != nil {
		q.Verdict, q.Blocking, q.NonBlocking = o.Verdict, o.Blocking, o.NonBlocking
	}
	if last.Record.State == record.UnrecordedState {
		q.LastMessage = last.Record.Resume
	}
	return q, true
}

// NoticeCase is what a needs-me row notifies of.
type NoticeCase string

const (
	NoticeRound    NoticeCase = "round"
	NoticeHeld     NoticeCase = "held"
	NoticeBlocked  NoticeCase = "blocked"
	NoticeQuestion NoticeCase = "question"
	NoticeWait     NoticeCase = "wait"
)

// Item is the id prefix of the derived item whose entry carries the case's
// notification; "" for a question, which the turn's Inbox message carries.
func (c NoticeCase) Item() string {
	switch c {
	case NoticeRound:
		return "gh:round"
	case NoticeHeld:
		return "gh:held"
	case NoticeBlocked:
		return "gh:blocked"
	case NoticeWait:
		return "gh:wait"
	}
	return ""
}

// Notice is what a needs-me row notifies; Attention carries no ID and no
// Category, which the caller keys.
type Notice struct {
	Case NoticeCase
	// Token lasts as long as what is notified: the pending round's since,
	// the held day, the blocking reason, the question's record, the waiting
	// episode.
	Token string
	// Record is the question's turn record.
	Record    int64
	Attention api.Attention
}

// Notice is what r, a row of this provider derived from in, notifies at
// d.Now; ok is false when it notifies nothing.
func (d Deriver) Notice(r api.Row, in any) (Notice, bool) {
	started := false
	if x, ok := in.(*input); ok && x.turn != nil {
		started = x.turn.Facts.State == string(turn.Started)
	}
	return d.notice(r, started)
}

// Attention is the notification of r, a row of needs-me derived from in, nil
// when it notifies nothing. A held round's id holds the day.
func (d Deriver) Attention(r api.Row, in any) *api.Attention {
	var started bool
	if x, has := in.(*input); has && x.turn != nil {
		started = x.turn.Facts.State == string(turn.Started)
	} else {
		f, found := d.Turns[refOf(r)]
		started = found && f.Facts.State == string(turn.Started)
	}
	n, ok := d.notice(r, started)
	if !ok {
		return nil
	}
	a := n.Attention
	switch n.Case {
	case NoticeRound:
		a.Category = categoryRound
	case NoticeHeld:
		a.Category = categoryHeld
	case NoticeBlocked:
		a.Category = categoryBlocked
	case NoticeQuestion:
		a.Category = "Q" + strconv.FormatInt(n.Record, 10)
	case NoticeWait:
		a.Category = categoryWait
	}
	idCategory := a.Category
	if n.Case == NoticeBlocked {
		idCategory = categoryAsk
	}
	a.ID = notificationID(idCategory, refOf(r), n.Token)
	return &a
}

func (d Deriver) notice(r api.Row, started bool) (Notice, bool) {
	ref := refOf(r)
	switch {
	case r.Pending != nil:
		return Notice{Case: NoticeRound, Token: r.Pending.Since.UTC().Format(stampToken), Attention: api.Attention{
			Title: "Round pending", Subtitle: prLabel(ref),
			Body:  r.Pending.Intent + ": " + r.Pending.Reason + " · waiting " + span(d.Now.Sub(r.Summary.Since)) + " · " + r.Title,
			Image: "round", Actions: d.notificationActions(r),
		}}, true
	case situation.Kind(r.Kind) == situation.KindHeldByCap:
		return Notice{Case: NoticeHeld, Token: d.Now.Local().Format(time.DateOnly), Attention: api.Attention{
			Title: "Round held", Subtitle: prLabel(ref), Body: r.Summary.Reason + " · " + r.Title, Image: "held",
			Actions: d.notificationActions(r),
		}}, true
	case situation.Kind(r.Kind) == situation.KindBlocked:
		return Notice{Case: NoticeBlocked, Token: reasonToken(r.Summary.Reason), Attention: api.Attention{
			Title: "Round blocked", Subtitle: prLabel(ref), Body: r.Summary.Reason + " · " + r.Title, Image: "blocked",
			Actions: d.notificationActions(r),
		}}, true
	case (situation.Kind(r.Kind) == situation.KindLoopWaiting || situation.Kind(r.Kind) == situation.KindLoopStopped) && r.Question != nil:
		return d.questionNotice(r), true
	case situation.Kind(r.Kind) == situation.KindLoopWaiting:
		title := "Agent asks"
		if started {
			title = "Waiting for a permission"
		}
		return Notice{Case: NoticeWait, Token: r.Summary.Since.UTC().Format(stampToken) + "." + reasonToken(r.Summary.Reason), Attention: api.Attention{
			Title: title, Subtitle: prLabel(ref), Body: r.Summary.Reason + " · " + r.Title, Image: "ask",
			Actions: d.notificationActions(r),
		}}, true
	}
	return Notice{}, false
}

func (d Deriver) questionNotice(r api.Row) Notice {
	ref := refOf(r)
	q := r.Question
	title := "Agent asks"
	if q.State != string(turn.Asked) {
		title = "Agent stopped"
	}
	body := []string{r.Summary.Reason}
	if q.Verdict != "" {
		body = append(body, fmt.Sprintf("drafted %s, %d blocking, %d non-blocking", q.Verdict, q.Blocking, q.NonBlocking))
	}
	if label := recommendedLabel(q); label != "" {
		body = append(body, "recommends: "+label)
	}
	body = append(body, r.Title)
	return Notice{Case: NoticeQuestion, Token: strconv.FormatInt(q.RecordID, 10), Record: q.RecordID, Attention: api.Attention{
		Title: title, Subtitle: prLabel(ref), Body: strings.Join(body, " · "), Image: "ask",
		Actions: d.notificationActions(r),
	}}
}

func recommendedLabel(q *api.QuestionView) string {
	switch {
	case q.Recommend == "":
		return ""
	case q.Kind == "confirm" && q.Recommend == "yes":
		return "Yes"
	case q.Kind == "confirm":
		return "No"
	}
	if i := slices.IndexFunc(q.Options, func(o record.Option) bool { return o.ID == q.Recommend }); i >= 0 {
		return q.Options[i].Label
	}
	return ""
}

func choiceID(q *api.QuestionView, answer string) string {
	if q.Kind == "confirm" {
		return "answer:" + answer
	}
	return "answer:option:" + answer
}

// answerChoices are the one-tap answers: each option, or Yes and No for a
// confirm, the recommended one first.
func answerChoices(q *api.QuestionView) []api.AttentionAction {
	var out []api.AttentionAction
	if q.Kind == "confirm" {
		out = []api.AttentionAction{{ID: "answer:yes", Title: "Yes"}, {ID: "answer:no", Title: "No"}}
	} else {
		for _, o := range q.Options {
			out = append(out, api.AttentionAction{ID: choiceID(q, o.ID), Title: o.Label})
		}
	}
	if q.Recommend == "" {
		return out
	}
	if i := slices.IndexFunc(out, func(a api.AttentionAction) bool { return a.ID == choiceID(q, q.Recommend) }); i >= 0 {
		rec := out[i]
		rec.Title += " (recommended)"
		out = append([]api.AttentionAction{rec}, slices.Delete(out, i, i+1)...)
	}
	return out
}

const stampToken = "20060102T150405.000000000"

// notificationID carries the pull request, so an answer still finds it
// after the menu bar restarts.
func notificationID(category string, ref pr.Ref, token string) string {
	return fmt.Sprintf("%s|%s|%s|%d|%s", category, ref.Owner, ref.Repo, ref.Number, token)
}

// reasonToken keys a notification that lasts as long as its reason, so a
// restart replaces it instead of adding another.
func reasonToken(reason string) string {
	h := fnv.New64a()
	h.Write([]byte(reason))
	return strconv.FormatUint(h.Sum64(), 36)
}

// refOf is the pull request of a row of this provider.
func refOf(r api.Row) pr.Ref {
	ref, _ := pr.ParseKey(r.Unit.Key)
	return ref
}

func prLabel(ref pr.Ref) string {
	return fmt.Sprintf("%s/%s #%d", ref.Owner, ref.Repo, ref.Number)
}

// span is a duration as a notification says it: "20 min", "3h", "2d".
func span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}
