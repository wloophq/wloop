package slack

import (
	"bytes"
	"cmp"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/auth"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/draft"
	"github.com/wloophq/wloop/plugins/slack/send"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// The tools of the plugin's manifest.
const (
	ToolPost   = "slack_post"
	ToolRead   = "slack_read"
	ToolSearch = "slack_search"
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
	case ToolPost:
		return t.post(ctx, call.Args), nil
	case ToolRead:
		return t.read(ctx, call.Args), nil
	case ToolSearch:
		return t.search(ctx, call.Args), nil
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

//go:embed plugin.toml drafts
var pluginFiles embed.FS

// draftKind is a kind the manifest declares: what slack_post writes and how
// it reaches Slack.
type draftKind struct {
	name, purpose string
	delivery      draft.Delivery
}

// declaredKinds are the manifest's draft kinds, in its order; the first is
// slack_post's default.
var declaredKinds = func() []draftKind {
	raw, err := pluginFiles.ReadFile(manifest.File)
	if err != nil {
		panic(err)
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		panic(err)
	}
	m.Files = pluginFiles
	s, err := m.Source()
	if err != nil {
		panic(err)
	}
	out := make([]draftKind, len(s.DraftKinds))
	for i, k := range s.DraftKinds {
		out[i] = draftKind{name: k.Name, purpose: cmp.Or(k.Purpose, k.Name), delivery: draft.Delivery(cmp.Or(k.Delivery, s.DefaultDelivery))}
	}
	return out
}()

// draftKinds are the names of the kinds slack_post offers.
var draftKinds = func() []string {
	out := make([]string, len(declaredKinds))
	for i, k := range declaredKinds {
		out[i] = k.name
	}
	return out
}()

func kindNamed(name string) (draftKind, bool) {
	i := slices.IndexFunc(declaredKinds, func(k draftKind) bool { return k.name == name })
	if i < 0 {
		return draftKind{}, false
	}
	return declaredKinds[i], true
}

// post writes a Slack post as the user: a draft for the user, or, with draft false, posted at once when the user
// drives the session, which the host decides.
func (t tools) post(ctx context.Context, raw json.RawMessage) src.ToolResult {
	var a struct {
		Text      *string         `json:"text"`
		Kind      string          `json:"kind"`
		Target    string          `json:"target"`
		Items     []string        `json:"items"`
		Reaction  string          `json:"reaction"`
		Effects   []string        `json:"effects"`
		Broadcast *bool           `json:"broadcast"`
		Payload   json.RawMessage `json:"payload"`
		Basis     string          `json:"basis"`
		Draft     *bool           `json:"draft"`
		Hold      bool            `json:"hold"`
	}
	if err := decodeStrict(raw, &a); err != nil {
		return badArguments(err)
	}
	if p := bytes.TrimSpace(a.Payload); len(p) > 0 && p[0] != '{' && !bytes.Equal(p, []byte("null")) {
		return badArguments(errors.New("payload: an object"))
	}
	if t.session == "" {
		return answer("refused", fields{"Error": "the call names no session; slack_post writes for a session"}, true)
	}
	now := a.Draft != nil && !*a.Draft
	d := draft.Draft{Kind: draft.Kind(cmp.Or(strings.TrimSpace(a.Kind), draftKinds[0])), Target: strings.TrimSpace(a.Target), Items: a.Items,
		Reaction: strings.TrimSpace(a.Reaction), Effects: a.Effects, Payload: a.Payload, Basis: a.Basis, Hold: a.Hold}
	if a.Broadcast != nil {
		d.Broadcast = draft.BroadcastOff
		if *a.Broadcast {
			d.Broadcast = draft.BroadcastOn
		}
	}
	k, ok := kindNamed(string(d.Kind))
	if !ok {
		return fixThese([]Problem{{Answer: "problem_kind", Fields: fields{"Kinds": strings.Join(draftKinds, ", ")}}})
	}
	d.Purpose, d.Delivery = k.purpose, k.delivery
	if a.Text != nil && d.Delivery != draft.DeleteDelivery {
		d.Text = *a.Text
	}
	var problems []Problem
	if a.Hold && now {
		problems = append(problems, Problem{Answer: "problem_hold"})
	}
	if d.Delivery.ChangesAMessage() && !send.IsMessage(d.Target) {
		problems = append(problems, Problem{Answer: "problem_message"})
	}
	switch {
	case d.Delivery.Emoji() && d.Reaction == "":
		problems = append(problems, Problem{Answer: "problem_reaction_required"})
	case !d.Delivery.Emoji() && d.Delivery != draft.DeleteDelivery && strings.TrimSpace(d.Text) == "":
		problems = append(problems, Problem{Answer: "problem_text_required"})
	}
	if len(problems) > 0 {
		return fixThese(problems)
	}
	s, ok := t.sender()
	if !ok {
		return notLoggedIn()
	}
	if d.Delivery.ChangesAMessage() {
		problem, err := t.changed(ctx, s, &d)
		switch {
		case err != nil:
			return failed(err)
		case problem != "":
			return fixThese([]Problem{{Answer: problem}})
		}
	}
	res, err := t.propose(ctx, s, d, now)
	if err != nil {
		return failed(err)
	}
	return res
}

// changed reads the message an edit or a deletion d changes, so its card
// shows the text it replaces or removes: a problem when Slack holds no such
// message or the user did not post it.
func (t tools) changed(ctx context.Context, s *send.Service, d *draft.Draft) (problem string, err error) {
	tok, err := t.p.slack.Tokens.Load()
	if err != nil {
		return "", err
	}
	m, ok, err := s.Read(ctx, d.Target)
	switch {
	case err != nil:
		return "", err
	case !ok:
		return "problem_message_gone", nil
	case m.User != tok.User:
		return "problem_message_not_yours", nil
	}
	if d.Delivery == draft.DeleteDelivery {
		d.Text = m.Text
	} else {
		d.Replaced = m.Text
	}
	return "", nil
}

// propose opens the gate of d for the session, posting it at once with now: on the unit of the session's turn,
// or on the unit of its message target when that is another or the session has no turn, which answers none of
// the turn's items and declares no effect; with no turn and no such unit, it names no unit and the host places
// it.
func (t tools) propose(ctx context.Context, s *send.Service, d draft.Draft, now bool) (src.ToolResult, error) {
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
	wire := WireDraft(d)
	if noTurn && d.Unit == turnUnit {
		wire.Unit = sdk.Unit{}
	}
	ref, err := h.Gates.OpenGate(ctx, src.GateRequest{Draft: wire, Session: t.session, Post: now})
	if err != nil {
		return src.ToolResult{}, err
	}
	where := ""
	if pub, err := p.publisher(); err == nil {
		where, _ = pub.Where(ctx, d)
	}
	return answer(postAnswer(now, draft.State(ref.State)), fields{"ID": ref.Draft, "Unit": unitName(wire.Unit, d.Unit), "Where": where, "Link": ref.Link}, false), nil
}

// unitName is the unit a draft is on as its answer names it: "" for one that named none.
func unitName(wire sdk.Unit, u unit.Unit) string {
	if wire == (sdk.Unit{}) {
		return ""
	}
	return u.String()
}

// postAnswer is slack_post's answer for a post whose draft its gate left in state st, now when it asked to post
// at once: posting once it is on its way, kept when it waits for the user, drafted when it was asked to, approved
// by a rule of the user's, dry_run under a dry run.
func postAnswer(now bool, st draft.State) string {
	switch {
	case st == draft.Dropped:
		return "dry_run"
	case now && st == draft.Held:
		return "kept"
	case now:
		return "posting"
	case st == draft.Queued || st == draft.Sending || st == draft.Sent:
		return "approved"
	}
	return "drafted"
}
