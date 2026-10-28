package slack

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/auth"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/draft"
	"github.com/wloophq/wloop/plugins/slack/send"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// The tools of the plugin's manifest.
const (
	ToolSend   = "slack_send_message"
	ToolRead   = "slack_read"
	ToolSearch = "slack_search"
	ToolDraft  = "slack_draft"
	ToolReact  = "slack_react"
	ToolEdit   = "slack_edit"
)

const (
	readLimit      = 50
	readLimitMax   = 200
	searchLimit    = 20
	searchLimitMax = 100
)

// broadcastToken is a Slack special mention that notifies a whole channel or group.
var broadcastToken = regexp.MustCompile(`(?i)<!(?:channel|here|everyone|subteam\^)`)

var slackToken = regexp.MustCompile(`(?:xox[a-z]\.)?(?:xox[a-z]|xapp)-[A-Za-z0-9-]+`)

func redactTokens(s string) string { return slackToken.ReplaceAllString(s, "[redacted]") }

// fields are an answer's fields, which the host's tools.toml words.
type fields map[string]any

func answer(name string, f fields, isError bool) src.ToolResult {
	r := src.ToolResult{Answer: name, IsError: isError}
	if len(f) > 0 {
		r.Fields, _ = json.Marshal(f)
	}
	return r
}

// Problem is one problem of a fix_these answer: an answer of the tool,
// which the host words, and its fields.
type Problem struct {
	Answer string `json:"answer"`
	Fields fields `json:"fields,omitempty"`
}

func fixThese(problems []Problem) src.ToolResult {
	return answer("fix_these", fields{"Problems": problems}, true)
}

func badArguments(err error) src.ToolResult {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return answer("bad_arguments", fields{"Error": msg}, true)
}

func notLoggedIn() src.ToolResult { return answer("not_logged_in", nil, true) }

func failed(err error) src.ToolResult {
	if errors.Is(err, send.ErrNotLoggedIn) || errors.Is(err, auth.ErrNotLoggedIn) {
		return notLoggedIn()
	}
	return answer("failed", fields{"Error": redactTokens(err.Error())}, true)
}

func decodeStrict(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.Decode(new(json.RawMessage)) != io.EOF {
		return errors.New("text after the arguments")
	}
	return nil
}

// CallTool runs one of the manifest's tools for the session call.Session.
func (s *Plugin) CallTool(ctx context.Context, call src.ToolCall) (src.ToolResult, error) {
	p, err := s.running()
	if err != nil {
		return src.ToolResult{}, err
	}
	t := tools{p: p, session: call.Session}
	switch call.Tool {
	case ToolSend:
		return t.send(ctx, call.Args), nil
	case ToolRead:
		return t.read(ctx, call.Args), nil
	case ToolSearch:
		return t.search(ctx, call.Args), nil
	case ToolDraft:
		return t.draft(ctx, call.Args), nil
	case ToolReact:
		return t.react(ctx, call.Args), nil
	case ToolEdit:
		return t.edit(ctx, call.Args), nil
	}
	return src.ToolResult{}, src.ErrNotFound
}

// tools are one call's tools.
type tools struct {
	p       *plugin
	session string
}

func (t tools) sender() (*send.Service, bool) {
	p := t.p
	if p.slack.Client == nil || p.slack.Tokens == nil {
		return nil, false
	}
	if _, err := p.slack.Tokens.Load(); errors.Is(err, auth.ErrNotLoggedIn) {
		return nil, false
	}
	s := &send.Service{Client: p.slack.Client, Log: p.log, PostAllowlist: p.cfg.PostAllowlist}
	if p.db != nil {
		s.Stored = StoredOver(p.db, p.log)
	}
	return s, true
}

func (t tools) send(ctx context.Context, raw json.RawMessage) src.ToolResult {
	var a struct {
		Target    *string `json:"target"`
		Text      *string `json:"text"`
		Thread    *bool   `json:"thread"`
		Broadcast bool    `json:"broadcast"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return badArguments(err)
	}
	var problems []Problem
	if a.Target == nil || strings.TrimSpace(*a.Target) == "" {
		problems = append(problems, Problem{Answer: "problem_target_required"})
	}
	if a.Text == nil || strings.TrimSpace(*a.Text) == "" {
		problems = append(problems, Problem{Answer: "problem_text_required"})
	}
	if len(problems) > 0 {
		return fixThese(problems)
	}
	if broadcastToken.MatchString(*a.Text) {
		return answer("broadcast", nil, true)
	}
	s, ok := t.sender()
	if !ok {
		return notLoggedIn()
	}
	req := send.Request{Target: strings.TrimSpace(*a.Target), Text: *a.Text, Thread: a.Thread, Broadcast: a.Broadcast, Source: "session"}
	sent, err := s.Send(ctx, req)
	if err != nil {
		return failed(err)
	}
	t.recordPost(ctx, req, sent)
	return answer("posted", fields{"Conversation": sent.Conversation, "Parent": sent.Parent, "Permalink": sent.Permalink,
		"Channel": sent.Channel, "TS": sent.TS, "Text": sent.Text}, false)
}

func (t tools) react(ctx context.Context, raw json.RawMessage) src.ToolResult {
	var a struct {
		Message *string `json:"message"`
		Emoji   *string `json:"emoji"`
		Remove  bool    `json:"remove"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return badArguments(err)
	}
	msg, emoji, refusal := actArgs("problem_emoji_required", a.Message, a.Emoji)
	if refusal != nil {
		return *refusal
	}
	s, ok := t.sender()
	if !ok {
		return notLoggedIn()
	}
	r := send.Reaction{Message: msg, Name: strings.Trim(strings.TrimSpace(emoji), ":"), Remove: a.Remove}
	got, err := s.React(ctx, r)
	if err != nil {
		return failed(err)
	}
	if !got.Unchanged {
		t.recordReaction(ctx, r, got)
	}
	name := "reacted"
	switch {
	case a.Remove && got.Unchanged:
		name = "absent"
	case a.Remove:
		name = "removed"
	case got.Unchanged:
		name = "already"
	}
	return answer(name, fields{"Emoji": got.Name, "Conversation": got.Conversation, "Message": send.Ref(got.Channel, got.TS),
		"Permalink": got.Permalink}, false)
}

func (t tools) edit(ctx context.Context, raw json.RawMessage) src.ToolResult {
	var a struct {
		Message *string `json:"message"`
		Text    *string `json:"text"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return badArguments(err)
	}
	msg, body, refusal := actArgs("problem_text_required", a.Message, a.Text)
	if refusal != nil {
		return *refusal
	}
	if broadcastToken.MatchString(body) {
		return answer("broadcast", nil, true)
	}
	s, ok := t.sender()
	if !ok {
		return notLoggedIn()
	}
	tok, err := t.p.slack.Tokens.Load()
	if err != nil {
		return failed(err)
	}
	e := send.Edit{Message: msg, Text: body, User: tok.User}
	got, err := s.Edit(ctx, e)
	if err != nil {
		return failed(err)
	}
	t.recordEdit(ctx, e, got)
	return answer("edited", fields{"Conversation": got.Conversation, "Message": send.Ref(got.Channel, got.TS), "Permalink": got.Permalink,
		"Text": got.Text, "Replaced": got.Replaced}, false)
}

// actArgs checks the arguments of slack_react or slack_edit: a message ref
// or permalink and the other argument, whose absence is the answer missing.
func actArgs(missing string, msg, value *string) (string, string, *src.ToolResult) {
	var problems []Problem
	m := ""
	if msg != nil {
		m = strings.TrimSpace(*msg)
	}
	if !send.IsMessage(m) {
		problems = append(problems, Problem{Answer: "problem_message"})
	}
	if value == nil || strings.TrimSpace(*value) == "" {
		problems = append(problems, Problem{Answer: missing})
	}
	if len(problems) > 0 {
		r := fixThese(problems)
		return "", "", &r
	}
	return m, *value, nil
}

func (t tools) read(ctx context.Context, raw json.RawMessage) src.ToolResult {
	var a struct {
		Target *string         `json:"target"`
		Limit  json.RawMessage `json:"limit"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return badArguments(err)
	}
	var problems []Problem
	if a.Target == nil || strings.TrimSpace(*a.Target) == "" {
		problems = append(problems, Problem{Answer: "problem_target_required"})
	}
	limit := readLimit
	if a.Limit != nil {
		var n int
		if err := json.Unmarshal(a.Limit, &n); err != nil || n < 1 || n > readLimitMax {
			problems = append(problems, Problem{Answer: "problem_limit", Fields: fields{"Max": readLimitMax}})
		}
		limit = n
	}
	if len(problems) > 0 {
		return fixThese(problems)
	}
	if _, ok := t.sender(); !ok || t.p.db == nil {
		return notLoggedIn()
	}
	out, err := ReaderOver(t.p.db, t.p.slack.Client, t.p.log).Read(ctx, strings.TrimSpace(*a.Target), limit)
	if err != nil {
		return failed(err)
	}
	return answer("read", fields{"Text": redactTokens(out)}, false)
}

func (t tools) search(ctx context.Context, raw json.RawMessage) src.ToolResult {
	var a struct {
		Query   *string         `json:"query"`
		Channel string          `json:"channel"`
		From    string          `json:"from"`
		After   string          `json:"after"`
		Before  string          `json:"before"`
		Limit   json.RawMessage `json:"limit"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return badArguments(err)
	}
	q := SearchQuery{Channel: strings.TrimSpace(a.Channel), From: strings.TrimSpace(a.From), After: strings.TrimSpace(a.After),
		Before: strings.TrimSpace(a.Before), Limit: searchLimit}
	if a.Query != nil {
		q.Query = strings.TrimSpace(*a.Query)
	}
	var problems []Problem
	if q.Query == "" {
		problems = append(problems, Problem{Answer: "problem_query_required"})
	}
	after, afterOK := searchDate(q.After)
	before, beforeOK := searchDate(q.Before)
	if !afterOK {
		problems = append(problems, Problem{Answer: "problem_after"})
	}
	if !beforeOK {
		problems = append(problems, Problem{Answer: "problem_before"})
	}
	if afterOK && beforeOK && !after.IsZero() && !before.IsZero() && !before.After(after) {
		problems = append(problems, Problem{Answer: "problem_before_after"})
	}
	if a.Limit != nil {
		var n int
		if err := json.Unmarshal(a.Limit, &n); err != nil || n < 1 || n > searchLimitMax {
			problems = append(problems, Problem{Answer: "problem_limit", Fields: fields{"Max": searchLimitMax}})
		}
		q.Limit = n
	}
	if len(problems) > 0 {
		return fixThese(problems)
	}
	if _, ok := t.sender(); !ok || t.p.db == nil {
		return notLoggedIn()
	}
	out, err := ReaderOver(t.p.db, t.p.slack.Client, t.p.log).Search(ctx, q)
	if err != nil {
		return failed(err)
	}
	return answer("found", fields{"Text": redactTokens(out)}, false)
}

// searchDate is s as a date; ok is false when s is set and not YYYY-MM-DD.
func searchDate(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, true
	}
	d, err := time.Parse(time.DateOnly, s)
	return d, err == nil
}

// draftKinds are the kinds slack_draft offers; a react posts no message.
var draftKinds = func() []string {
	out := make([]string, len(draft.ConversationKinds))
	for i, k := range draft.ConversationKinds {
		out[i] = string(k)
	}
	return out
}()

var errNoDraftTurn = errors.New("this session has no turn; slack_draft drafts on the unit of a turn sent to or started in this session")

func (t tools) draft(ctx context.Context, raw json.RawMessage) src.ToolResult {
	var a struct {
		Text     *string  `json:"text"`
		Kind     string   `json:"kind"`
		Target   string   `json:"target"`
		Items    []string `json:"items"`
		Reaction string   `json:"reaction"`
		Effects  []string `json:"effects"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return badArguments(err)
	}
	if t.session == "" {
		return answer("refused", fields{"Error": "the call names no session; slack_draft drafts for a session"}, true)
	}
	d := draft.Draft{Kind: draft.Kind(cmp.Or(strings.TrimSpace(a.Kind), draftKinds[0])), Target: strings.TrimSpace(a.Target), Items: a.Items,
		Reaction: strings.TrimSpace(a.Reaction), Effects: a.Effects}
	if a.Text != nil {
		d.Text = *a.Text
	}
	switch {
	case !slices.Contains(draftKinds, string(d.Kind)):
		return fixThese([]Problem{{Answer: "problem_kind", Fields: fields{"Kinds": strings.Join(draftKinds, ", ")}}})
	case d.Kind.Emoji() && d.Reaction == "":
		return fixThese([]Problem{{Answer: "problem_reaction_required"}})
	case !d.Kind.Emoji() && strings.TrimSpace(d.Text) == "":
		return fixThese([]Problem{{Answer: "problem_text_required"}})
	}
	s, ok := t.sender()
	if !ok {
		return notLoggedIn()
	}
	res, err := t.propose(ctx, s, d)
	if err != nil {
		return failed(err)
	}
	return res
}

// propose opens the gate of d for the session: on the unit of the
// session's turn, or on the unit of its message target when that is
// another or the session has no turn, which answers none of the turn's
// items and declares no effect.
func (t tools) propose(ctx context.Context, s *send.Service, d draft.Draft) (src.ToolResult, error) {
	p := t.p
	if d.Text != "" {
		text, err := s.Mentions(ctx, d.Text, func(user string) string { return "@{" + string(dslack.Provider) + ":" + user + "}" })
		if err != nil {
			return src.ToolResult{}, err
		}
		d.Text = text
	}
	h := p.host.Host
	if h.Reader == nil || h.Gates == nil {
		return src.ToolResult{}, src.ErrUnsupported
	}
	sess, err := h.Reader.Session(ctx, t.session)
	if err != nil {
		return src.ToolResult{}, err
	}
	noTurn := sess.Unit == (src.Session{}).Unit
	turnUnit := unit.Of(sess.Unit)
	d.Unit = turnUnit
	if send.IsMessage(d.Target) {
		u, ok, err := p.resolve(ctx, d.Target)
		if err != nil {
			return src.ToolResult{}, err
		}
		if ok && (noTurn || u != turnUnit) {
			switch {
			case len(d.Items) > 0:
				return src.ToolResult{}, errors.New("items: a draft outside the turn's Slack unit answers none of its items")
			case len(d.Effects) > 0:
				return src.ToolResult{}, errors.New("effects: a draft outside the turn's Slack unit declares none")
			}
			d.Unit = u
		}
	}
	if noTurn && d.Unit == turnUnit {
		return src.ToolResult{}, errNoDraftTurn
	}
	d.Purpose, d.Delivery = draft.ShapeOf(d.Kind)
	ref, err := h.Gates.OpenGate(ctx, src.GateRequest{Draft: WireDraft(d), Session: t.session})
	if err != nil {
		return src.ToolResult{}, err
	}
	where := ""
	if pub, err := p.publisher(); err == nil {
		where, _ = pub.Where(ctx, d)
	}
	return answer(proposedAnswer(draft.State(ref.State)), fields{"ID": ref.Draft, "Unit": d.Unit.String(), "Where": where}, false), nil
}

// proposedAnswer is slack_draft's answer for a draft the approval policy
// left in state st.
func proposedAnswer(st draft.State) string {
	switch st {
	case draft.Approved:
		return "approved"
	case draft.Discarded:
		return "dry_run"
	}
	return "proposed"
}

// The purposes of the posts a session makes itself.
const (
	sentPurpose      = "reply"
	reactedPurpose   = "react"
	unreactedPurpose = "unreact"
	editedPurpose    = "edit"
)

func (t tools) recordPost(ctx context.Context, req send.Request, sent send.Sent) {
	where := func(ctx context.Context, team string) (unit.Unit, error) {
		thread := cmp.Or(sent.ThreadTS, sent.TS)
		if t.p.db != nil {
			u, ok, err := LandingOver(t.p.db, t.p.log).UnitOf(ctx, team, sent.Channel, sent.TS, sent.ThreadTS)
			if err != nil || ok {
				return u, err
			}
		}
		return dslack.ThreadUnit(team, sent.Channel, thread), nil
	}
	t.record(ctx, ToolSend, where, draft.Draft{Purpose: sentPurpose, Delivery: draft.Message, Target: req.Target, Text: req.Text},
		draft.Evidence{Ref: send.Ref(sent.Channel, sent.TS), Permalink: sent.Permalink})
}

func (t tools) recordReaction(ctx context.Context, r send.Reaction, got send.Reacted) {
	purpose := reactedPurpose
	if r.Remove {
		purpose = unreactedPurpose
	}
	t.record(ctx, ToolReact, t.messageUnit(r.Message), draft.Draft{Purpose: purpose, Delivery: draft.Reaction, Target: r.Message, Reaction: got.Name},
		draft.Evidence{Ref: send.Ref(got.Channel, got.TS), Permalink: got.Permalink})
}

func (t tools) recordEdit(ctx context.Context, e send.Edit, got send.Edited) {
	t.record(ctx, ToolEdit, t.messageUnit(e.Message), draft.Draft{Purpose: editedPurpose, Delivery: draft.EditDelivery, Target: e.Message,
		Text: e.Text, Replaced: got.Replaced}, draft.Evidence{Ref: send.Ref(got.Channel, got.TS), Permalink: got.Permalink})
}

func (t tools) messageUnit(message string) func(context.Context, string) (unit.Unit, error) {
	return func(ctx context.Context, team string) (unit.Unit, error) {
		if t.p.db == nil {
			return FollowUnit(ctx, t.p.slack.Client, team, message)
		}
		return LandingOver(t.p.db, t.p.log).MessageUnit(ctx, t.p.slack.Client, team, message)
	}
}

// record keeps d, which a tool posted at once, as a sent draft on the unit
// where says the post landed in; it never changes or fails the post.
func (t tools) record(ctx context.Context, tool string, where func(ctx context.Context, team string) (unit.Unit, error), d draft.Draft,
	ev draft.Evidence) {
	p := t.p
	ctx = context.WithoutCancel(ctx)
	if p.host.Host.Gates == nil {
		return
	}
	tok, err := p.slack.Tokens.Load()
	if err != nil {
		p.logf("slack_sent_unrecorded message=%s error=%v", ev.Ref, err)
		return
	}
	u, err := where(ctx, tok.Team)
	if err != nil {
		p.logf("slack_sent_unrecorded message=%s error=%v", ev.Ref, err)
		return
	}
	d.Unit, d.Kind, d.State, d.Evidence = u, draft.KindOf(d.Purpose, d.Delivery), draft.Sent, ev
	ref, err := p.host.Host.Gates.RecordSent(ctx, WireDraft(d), t.session, tool)
	if err != nil {
		p.logf("slack_sent_unrecorded message=%s error=%v", ev.Ref, err)
		return
	}
	p.logf("slack_sent_recorded draft=%d unit=%s", ref.Draft, u)
}
