package slack

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	slackapi "github.com/wloophq/wloop/internal/slack"
	"github.com/wloophq/wloop/internal/slack/auth"
	"github.com/wloophq/wloop/internal/slack/send"
)

// mayHaveBeenPosted starts a Publish or Verify error when the post may have
// reached Slack; the outbox asks for a confirmation before a retry.
const mayHaveBeenPosted = "may have been posted"

const (
	// verifySkew widens Verify's window: Slack's clock stamps the ts.
	verifySkew     = 30 * time.Second
	verifyPages    = 10
	reactionsScope = "reactions:write"
)

// Publisher posts Slack drafts as the user: messages through send's
// resolution and post allowlist, reactions with reactions.add.
type Publisher struct {
	Send *send.Service
	// Token is the stored user token: its user and granted scopes.
	Token    func() (auth.Token, error)
	Segments interface {
		Range(ctx context.Context, u unit.Unit) (conv, fromTS, toTS string, err error)
	}
	// People are the persons the unit's items name, for mentions of other providers.
	People func(ctx context.Context, u unit.Unit) ([]items.Person, error)
	// Items are the unit's items, whose anchors place a draft on a DM segment.
	Items func(ctx context.Context, u unit.Unit) ([]items.Item, error)
	Now   func() time.Time
	Log   func(format string, args ...any)
}

var (
	_ port.Effects   = (*Publisher)(nil)
	_ port.Validator = (*Publisher)(nil)
	_ port.Locator   = (*Publisher)(nil)
	_ port.Placer    = (*Publisher)(nil)
)

// plan is where and what a draft posts.
type plan struct {
	res      send.Resolved
	text     string // mrkdwn; messages only
	reaction string // react only
	at       string // react only: the message the reaction goes on
}

func (pl plan) where() string {
	if pl.res.Parent != "" {
		return fmt.Sprintf("%s in the thread %q", pl.res.Conversation, pl.res.Parent)
	}
	return pl.res.Conversation
}

// Validate refuses, without posting, a draft Slack would not take.
func (p *Publisher) Validate(ctx context.Context, d draft.Draft) error {
	_, err := p.prepare(ctx, d)
	return err
}

// Where names the conversation d would post to, or, for a recorded edit, the
// conversation of the message it replaced.
func (p *Publisher) Where(ctx context.Context, d draft.Draft) (string, error) {
	if d.Delivery == draft.EditDelivery {
		if p.Send == nil || p.Send.Client == nil {
			return "", send.ErrNotLoggedIn
		}
		no := false
		res, err := p.Send.Resolve(ctx, send.Request{Target: d.Target, Thread: &no, Source: "draft"})
		return res.Conversation, err
	}
	pl, err := p.prepare(ctx, d)
	if err != nil {
		return "", err
	}
	return pl.where(), nil
}

func (p *Publisher) prepare(ctx context.Context, d draft.Draft) (plan, error) {
	k, err := dslack.ParseUnit(d.Unit)
	if err != nil {
		return plan{}, fmt.Errorf("slack publishes only slack units: %w", err)
	}
	if p.Send == nil || p.Send.Client == nil {
		return plan{}, send.ErrNotLoggedIn
	}
	if d.Delivery == draft.EditDelivery {
		return plan{}, errors.New("an edit goes out through slack_edit at once and is only recorded, never published from the Outbox")
	}
	var pl plan
	if d.Delivery == draft.Reaction {
		if pl.reaction = strings.Trim(strings.TrimSpace(d.Reaction), ":"); pl.reaction == "" {
			return plan{}, errors.New("a reaction needs an emoji name")
		}
		if err := p.canReact(); err != nil {
			return plan{}, err
		}
	} else if pl.text, err = p.body(ctx, d); err != nil {
		return plan{}, err
	} else if strings.TrimSpace(pl.text) == "" {
		return plan{}, errors.New("the message is empty")
	}
	if err := p.target(ctx, d, k, &pl); err != nil {
		return plan{}, err
	}
	if err := p.Send.Allowed(pl.res); err != nil {
		return plan{}, err
	}
	return pl, nil
}

func (p *Publisher) canReact() error {
	if p.Token == nil {
		return send.ErrNotLoggedIn
	}
	tok, err := p.Token()
	if errors.Is(err, auth.ErrNotLoggedIn) {
		return fmt.Errorf("%w: %w", send.ErrNotLoggedIn, err)
	}
	if err != nil {
		return err
	}
	if !slices.Contains(tok.Scopes, reactionsScope) {
		return fmt.Errorf("the Slack token lacks the %s scope; run slack login again to grant it", reactionsScope)
	}
	return nil
}

var (
	userIDRE      = regexp.MustCompile(`^[UW][A-Z0-9]{2,}$`)
	personKeyRE   = regexp.MustCompile(`^@\{` + string(dslack.Provider) + `:([UW][A-Z0-9]{2,})\}$`)
	unitPersonRE  = regexp.MustCompile(`^` + string(dslack.Provider) + `:([UW][A-Z0-9]{2,})$`)
	conversationR = regexp.MustCompile(`^[CGD][A-Z0-9]{2,}$`)
)

// userOf is the user id a person target names, "" when it names none.
func userOf(target string) string {
	t := strings.TrimPrefix(target, "@")
	if userIDRE.MatchString(t) {
		return t
	}
	for _, re := range []*regexp.Regexp{personKeyRE, unitPersonRE} {
		if m := re.FindStringSubmatch(target); m != nil {
			return m[1]
		}
	}
	return ""
}

// target resolves where d posts: its unit's thread when Target is "", else a
// message ref or permalink, a conversation or a person. A person's DM is opened only once the
// post allowlist holds them.
func (p *Publisher) target(ctx context.Context, d draft.Draft, k dslack.Key, pl *plan) error {
	t := strings.TrimSpace(d.Target)
	react := d.Delivery == draft.Reaction
	switch {
	case t == "" && k.Segment != "":
		return errors.New("a draft on a direct message segment needs a target; its placement is named when it is proposed")
	case t == "":
		if err := p.resolve(ctx, send.Request{Target: k.Conversation}, pl); err != nil {
			return err
		}
		pl.res.ThreadTS, pl.at = k.TS, k.TS
		return nil
	}
	if _, ts, _, err := parseMessage(t); err == nil {
		if react {
			no := false
			pl.at = ts
			return p.resolve(ctx, send.Request{Target: t, Thread: &no}, pl)
		}
		return p.resolve(ctx, send.Request{Target: t}, pl)
	}
	if react {
		return fmt.Errorf("a reaction needs a message ref or permalink target, not %q", t)
	}
	if conversationR.MatchString(t) || strings.HasPrefix(t, "#") {
		return p.resolve(ctx, send.Request{Target: t}, pl)
	}
	user := userOf(t)
	if len(p.Send.PostAllowlist) > 0 {
		switch {
		case user == "" && strings.HasPrefix(t, "@"):
			return fmt.Errorf("%w: name the person by user id or email, not %s, while slack.post_allowlist is set", send.ErrNotAllowed, t)
		case user == "" && strings.Contains(t, "@"):
			u, err := p.Send.Client.UserByEmail(ctx, t)
			if err != nil {
				return fmt.Errorf("looking up %s: %w", t, err)
			}
			user = u.ID
		}
		if user != "" && !slices.Contains(p.Send.PostAllowlist, user) {
			return fmt.Errorf("%w: the direct message with %s", send.ErrNotAllowed, user)
		}
	}
	if user != "" {
		t = user
	}
	return p.resolve(ctx, send.Request{Target: t}, pl)
}

// Place names where a draft with no target posts. A thread unit's draft posts
// in that thread (""). A DM segment has no thread of its own: a message
// answers the newest message its items anchor on, in that message's thread,
// which a top-level message starts; a reaction goes on the newest message an
// item started at, the counterpart's request. A message the user writes with
// no items goes to the conversation, as typing in the DM does.
func (p *Publisher) Place(ctx context.Context, d draft.Draft) (string, error) {
	k, err := dslack.ParseUnit(d.Unit)
	if err != nil || k.Segment == "" {
		return "", nil
	}
	react := d.Delivery == draft.Reaction
	if len(d.Items) == 0 {
		switch {
		case d.Origin.Kind != draft.OriginUser:
			return "", errors.New("a draft on a direct message segment names neither a target nor items whose messages place it")
		case react:
			return "", errors.New("a reaction on a direct message needs the message it goes on, and the conversation alone names none")
		}
		return k.Conversation, nil
	}
	if p.Items == nil {
		return "", errors.New("the items of a direct message segment cannot be read to place its draft")
	}
	its, err := p.Items(ctx, d.Unit)
	if err != nil {
		return "", fmt.Errorf("reading the items of %s: %w", d.Unit.Label(), err)
	}
	newest := ""
	for _, it := range its {
		if !slices.Contains(d.Items, it.ID) {
			continue
		}
		anchors := anchorsOf(it)
		if react {
			anchors = []unit.Anchor{it.Origin}
		}
		for _, a := range anchors {
			m, ok := send.ParseMessage(a.Ref)
			if ok && m.Conversation == k.Conversation && dslack.CompareTS(m.TS, newest) > 0 {
				newest = m.TS
			}
		}
	}
	if newest == "" {
		return "", fmt.Errorf("none of the items %s names a message of the direct message to answer; give the draft a target", strings.Join(d.Items, ", "))
	}
	return conversationLink(k.Conversation, newest), nil
}

// anchorsOf are the anchors an item's evidence names: where it started, was
// assigned and every message of its trail.
func anchorsOf(it items.Item) []unit.Anchor {
	out := []unit.Anchor{it.Origin, it.AssignedAt, it.Evidence.Anchor}
	for _, e := range it.Trail {
		out = append(out, e.Anchor)
	}
	return out
}

func (p *Publisher) resolve(ctx context.Context, r send.Request, pl *plan) error {
	r.Source = "draft"
	res, err := p.Send.Resolve(ctx, r)
	if err != nil {
		return err
	}
	pl.res = res
	return nil
}

var (
	mentionRE = regexp.MustCompile(`@\{([^{}\s]+)\}`)
	// typedTokenRE are the mention, channel and special tokens a draft's text spells out itself; only the
	// mentions body converts from @{slack:U...} reach Slack as tokens.
	typedTokenRE     = regexp.MustCompile(`<[@#!][^<>]*>`)
	broadcastRE      = regexp.MustCompile(`^<!(?:channel|here|everyone|subteam\^)`)
	plainBroadcastRE = regexp.MustCompile(`(^|[^\w@])@(channel|here|everyone)\b`)
)

// Placeholders hold converted mentions while Mrkdwn converts the rest, so a
// person's name is never read as Markdown or as a Slack token.
const (
	holdOpen  = "\uE010"
	holdClose = "\uE011"
)

var (
	stripHolds = strings.NewReplacer(holdOpen, "", holdClose, "")
	holdRE     = regexp.MustCompile(holdOpen + `(\d+)` + holdClose)
	nameEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "@", "@\u2060")
)

// body is d's text as Slack takes it: Mrkdwn, a slack mention as <@U...>,
// any other person by name, which pings nobody.
func (p *Publisher) body(ctx context.Context, d draft.Draft) (string, error) {
	var held []string
	hold := func(s string) string {
		held = append(held, s)
		return holdOpen + strconv.Itoa(len(held)-1) + holdClose
	}
	var names map[string]string
	var errs []error
	text := typedTokenRE.ReplaceAllStringFunc(stripHolds.Replace(d.Text), func(m string) string {
		if broadcastRE.MatchString(m) {
			errs = append(errs, fmt.Errorf("%s is a broadcast mention; drafts never ping a channel or a group", m))
			return m
		}
		return hold(nameEscape.Replace(m))
	})
	text = plainBroadcastRE.ReplaceAllString(text, "${1}@\u2060${2}")
	text = mentionRE.ReplaceAllStringFunc(text, func(m string) string {
		key := m[2 : len(m)-1]
		provider, id, ok := strings.Cut(key, ":")
		switch {
		case !ok || id == "":
			errs = append(errs, fmt.Errorf("mention %s is not @{<provider>:<id>}", m))
			return m
		case provider == string(dslack.Provider):
			if !userIDRE.MatchString(id) {
				errs = append(errs, fmt.Errorf("mention %s names no Slack user id", m))
				return m
			}
			return hold("<@" + id + ">")
		}
		if names == nil {
			var err error
			if names, err = p.names(ctx, d.Unit); err != nil {
				errs = append(errs, err)
				return m
			}
		}
		if n, ok := names[key]; ok {
			return hold(nameEscape.Replace(n))
		}
		if provider == "group" {
			return hold(nameEscape.Replace(id))
		}
		errs = append(errs, fmt.Errorf("mention %s names nobody the unit's items know", m))
		return m
	})
	if err := errors.Join(errs...); err != nil {
		return "", err
	}
	converted := send.Mrkdwn(text)
	if err := liveTokens(converted); err != nil {
		return "", err
	}
	return holdRE.ReplaceAllStringFunc(converted, func(m string) string {
		i, err := strconv.Atoi(holdRE.FindStringSubmatch(m)[1])
		if err != nil || i >= len(held) {
			return ""
		}
		return held[i]
	}), nil
}

// liveTokens refuses converted text that holds a Slack mention, channel or
// special token: the only ones a draft posts are the held mentions body converts.
func liveTokens(converted string) error {
	if m := typedTokenRE.FindAllString(converted, 3); len(m) > 0 {
		return fmt.Errorf("the converted text would post the Slack tokens %s; name people as @{slack:<user id>}", strings.Join(m, ", "))
	}
	return nil
}

func (p *Publisher) names(ctx context.Context, u unit.Unit) (map[string]string, error) {
	out := map[string]string{}
	if p.People == nil {
		return out, nil
	}
	people, err := p.People(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("reading the persons of %s: %w", u.Label(), err)
	}
	for _, x := range people {
		if x.Name == "" {
			continue
		}
		for _, k := range append([]string{x.Key}, x.Aliases...) {
			if _, ok := out[k]; !ok {
				out[k] = x.Name
			}
		}
	}
	return out, nil
}

// Publish posts d once. An error after the request may have reached Slack
// starts "may have been posted".
func (p *Publisher) Publish(ctx context.Context, d draft.Draft) (draft.Evidence, error) {
	pl, err := p.prepare(ctx, d)
	if err != nil {
		return draft.Evidence{}, err
	}
	c := p.Send.Client
	if pl.reaction != "" {
		err := c.AddReaction(ctx, pl.res.Channel, pl.at, pl.reaction)
		if err != nil && apiCode(err) != "already_reacted" {
			return draft.Evidence{}, p.failed(pl, err)
		}
		p.logf("slack_draft_published draft=%d delivery=%s channel=%s ts=%s", d.ID, d.Delivery, pl.res.Channel, pl.at)
		return p.evidence(ctx, pl.res.Channel, pl.at, p.now()), nil
	}
	posted, err := c.PostMessage(ctx, slackapi.PostArgs{Channel: pl.res.Channel, Text: pl.text, ThreadTS: pl.res.ThreadTS})
	if err != nil {
		return draft.Evidence{}, p.failed(pl, err)
	}
	if posted.TS == "" {
		return draft.Evidence{}, fmt.Errorf("%s to %s: Slack's answer names no message", mayHaveBeenPosted, pl.where())
	}
	channel := cmp.Or(posted.Channel, pl.res.Channel)
	p.logf("slack_draft_published draft=%d delivery=%s channel=%s ts=%s", d.ID, d.Delivery, channel, posted.TS)
	return p.evidence(ctx, channel, posted.TS, postedAt(posted.TS, p.now())), nil
}

// evidence names the message at ts in channel by its ref and permalink.
func (p *Publisher) evidence(ctx context.Context, channel, ts string, at time.Time) draft.Evidence {
	return draft.Evidence{Ref: send.Ref(channel, ts), Permalink: p.Send.Permalink(ctx, channel, ts), At: at}
}

func (p *Publisher) failed(pl plan, err error) error {
	if written(err) {
		return fmt.Errorf("%s to %s: %w", mayHaveBeenPosted, pl.where(), err)
	}
	return fmt.Errorf("posting to %s: %w", pl.where(), err)
}

// Verify looks for d posted by the user at or after startedAt, minus the
// clock skew. A failed read says where to check.
func (p *Publisher) Verify(ctx context.Context, d draft.Draft, startedAt time.Time) (draft.Evidence, bool, error) {
	pl, err := p.prepare(ctx, unplacedAsPosted(d))
	if err != nil {
		return draft.Evidence{}, false, p.unsure(ctx, d, pl, err)
	}
	if p.Token == nil {
		return draft.Evidence{}, false, p.unsure(ctx, d, pl, send.ErrNotLoggedIn)
	}
	tok, err := p.Token()
	if err != nil {
		return draft.Evidence{}, false, p.unsure(ctx, d, pl, err)
	}
	if pl.reaction != "" {
		rs, err := p.Send.Client.Reactions(ctx, pl.res.Channel, pl.at)
		if err != nil {
			return draft.Evidence{}, false, p.unsure(ctx, d, pl, err)
		}
		for _, r := range rs {
			if r.Name == pl.reaction && slices.Contains(r.Users, tok.User) {
				return p.evidence(ctx, pl.res.Channel, pl.at, p.now()), true, nil
			}
		}
		return draft.Evidence{}, false, nil
	}
	oldest := slackTSOf(startedAt.Add(-verifySkew))
	want := normalized(pl.text)
	args := slackapi.PageArgs{Oldest: oldest, Limit: 200}
	for range verifyPages {
		var page slackapi.MessagesPage
		if pl.res.ThreadTS != "" {
			page, err = p.Send.Client.Replies(ctx, pl.res.Channel, pl.res.ThreadTS, args)
		} else {
			page, err = p.Send.Client.History(ctx, pl.res.Channel, args)
		}
		if err != nil {
			return draft.Evidence{}, false, p.unsure(ctx, d, pl, err)
		}
		for _, raw := range page.Messages {
			m, err := dslack.MessageFrom(raw)
			if err != nil {
				continue
			}
			if m.User == tok.User && dslack.CompareTS(m.TS, oldest) >= 0 && normalized(m.Text) == want {
				return p.evidence(ctx, pl.res.Channel, m.TS, postedAt(m.TS, p.now())), true, nil
			}
		}
		if page.NextCursor == "" {
			break
		}
		args.Cursor = page.NextCursor
	}
	return draft.Evidence{}, false, nil
}

// unplacedAsPosted is d with the conversation as its target when it is a
// segment message sending with none: only a version that published such a
// draft at the conversation's top level, before placement, left it so.
func unplacedAsPosted(d draft.Draft) draft.Draft {
	if strings.TrimSpace(d.Target) != "" || d.Delivery == draft.Reaction {
		return d
	}
	if k, err := dslack.ParseUnit(d.Unit); err == nil && k.Segment != "" {
		d.Target = k.Conversation
	}
	return d
}

// unsure is Verify's error when it cannot tell: it names where to look.
func (p *Publisher) unsure(ctx context.Context, d draft.Draft, pl plan, err error) error {
	channel, ts := pl.res.Channel, pl.res.ThreadTS
	if channel == "" {
		if k, kerr := dslack.ParseUnit(d.Unit); kerr == nil {
			channel, ts = k.Conversation, k.TS
			if k.Segment != "" && p.Segments != nil {
				if conv, _, _, rerr := p.Segments.Range(ctx, d.Unit); rerr == nil {
					channel, ts = conv, ""
				}
			}
		}
	}
	return fmt.Errorf("%s; check %s (%w)", mayHaveBeenPosted, conversationLink(channel, ts), err)
}

func conversationLink(channel, ts string) string {
	if channel == "" {
		return "the conversation in Slack"
	}
	link := "https://slack.com/archives/" + channel
	if ts != "" {
		link += "/p" + strings.ReplaceAll(ts, ".", "")
	}
	return link
}

var (
	refTokenRE  = regexp.MustCompile(`<([@#!][^<>|\s]+)\|[^<>]*>`)
	linkTokenRE = regexp.MustCompile(`<((?:https?|mailto):[^<>|\s]+)(?:\|([^<>]*))?>`)
	spaceRE     = regexp.MustCompile(`\s+`)
	unescape    = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&")
)

// normalized is text as Verify compares it: labeled mentions bare, a link
// whose label is its URL as that label, entities decoded, whitespace collapsed.
func normalized(text string) string {
	text = refTokenRE.ReplaceAllString(text, "<$1>")
	text = linkTokenRE.ReplaceAllStringFunc(text, func(m string) string {
		parts := linkTokenRE.FindStringSubmatch(m)
		target, label := parts[1], parts[2]
		bare := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(target, "mailto:"), "https://"), "http://")
		switch label {
		case "":
			return strings.TrimPrefix(target, "mailto:")
		case target, bare:
			return label
		}
		return m
	})
	return strings.TrimSpace(spaceRE.ReplaceAllString(unescape.Replace(text), " "))
}

// written says whether a failed call may have reached Slack: anything but
// Slack's refusal, an HTTP 4xx, a failure to connect or one before the request left.
func written(err error) bool {
	var api *slackapi.APIError
	var ue *url.Error
	msg := err.Error()
	switch {
	case errors.As(err, &api), strings.Contains(msg, ": token: "):
		return false
	case strings.Contains(msg, ": read the response: "):
		return true
	case errors.As(err, &ue):
		var dns *net.DNSError
		var op *net.OpError
		switch {
		case errors.As(err, &dns):
			return false
		case errors.As(err, &op):
			return op.Op != "dial" && op.Op != "proxyconnect"
		}
		return true
	case errors.Is(err, auth.ErrNotLoggedIn) || errors.Is(err, send.ErrNotLoggedIn) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return false
	}
	if m := httpStatusRE.FindStringSubmatch(msg); m != nil {
		return m[1][0] != '4'
	}
	return true
}

var httpStatusRE = regexp.MustCompile(`: HTTP (\d{3})$`)

func apiCode(err error) string {
	var api *slackapi.APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

func slackTSOf(t time.Time) string {
	return fmt.Sprintf("%d.%06d", t.Unix(), t.Nanosecond()/1000)
}

func postedAt(ts string, fallback time.Time) time.Time {
	if t := tsTime(ts); !t.IsZero() {
		return t
	}
	return fallback
}

func (p *Publisher) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Publisher) logf(format string, args ...any) {
	if p.Log != nil {
		p.Log(format, args...)
	}
}

// publisher is the module's Publisher; nil without a store, a client or a token store.
func (p *plugin) publisher() port.Effects {
	if p.src == nil || p.slack.Client == nil || p.slack.Tokens == nil {
		return nil
	}
	return PublisherOver(p.deps.DB, &send.Service{Client: p.slack.Client, Log: p.log, PostAllowlist: p.cfg.PostAllowlist, Stored: StoredOver(p.deps.DB, p.log)},
		p.slack.Tokens.Load, p.now, p.log)
}

// PublisherOver publishes through s, with the segments and the items' persons db stores.
func PublisherOver(db *sqlite.DB, s *send.Service, token func() (auth.Token, error), now func() time.Time, log func(string, ...any)) *Publisher {
	st := db.Items()
	load := func(ctx context.Context, u unit.Unit) ([]items.Item, error) {
		byUnit, err := st.Load(ctx, []unit.Unit{u})
		return byUnit[u], err
	}
	return &Publisher{Send: s, Token: token, Segments: Segments{Store: db.Segments()}, Now: now, Log: log, Items: load,
		People: func(ctx context.Context, u unit.Unit) ([]items.Person, error) {
			its, err := load(ctx, u)
			if err != nil {
				return nil, err
			}
			var out []items.Person
			for _, it := range its {
				out = append(append(append(out, it.Requester), it.Assignees...), it.WaitingOn...)
			}
			return out, nil
		}}
}

// Landing finds the tracked unit a post outside the outbox landed in.
type Landing struct {
	Facts    dslack.FactsReader
	Segments dslack.Segments
	// Messages finds the thread of a stored message; nil asks Slack.
	Messages send.Stored
}

// LandingOver reads the facts and segments the Slack module stores in db.
func LandingOver(db *sqlite.DB, log func(string, ...any)) Landing {
	segs := Segments{Store: db.Segments()}
	return Landing{Facts: &Store{DB: db.Slack(), Segments: segs}, Segments: segs, Messages: StoredOver(db, log)}
}

// UnitOf is the unit of a post in team at ts in channel, in the thread
// threadTS ("" when none): the DM segment covering its anchor, or its channel
// thread when the facts store it. It reads only the store; false when untracked.
func (l Landing) UnitOf(ctx context.Context, team, channel, ts, threadTS string) (unit.Unit, bool, error) {
	anchor := cmp.Or(threadTS, ts)
	if channel == "" || anchor == "" {
		return unit.Unit{}, false, nil
	}
	c, err := l.Facts.Conversation(ctx, channel)
	if dm := err == nil && c.Kind.DM() || err != nil && strings.HasPrefix(channel, "D"); dm {
		units, err := l.Segments.UnitsFor(ctx, channel, anchor)
		if err != nil || len(units) == 0 {
			return unit.Unit{}, false, err
		}
		return units[0], true, nil
	}
	if team == "" {
		return unit.Unit{}, false, nil
	}
	u := dslack.ThreadUnit(team, channel, anchor)
	if _, err := l.Facts.Messages(ctx, u, "", 1); errors.Is(err, dslack.ErrUnknownUnit) {
		return unit.Unit{}, false, nil
	} else if err != nil {
		return unit.Unit{}, false, err
	}
	return u, true, nil
}
