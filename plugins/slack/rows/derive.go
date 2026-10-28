package slackrows

import (
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/store"
)

// The kinds of a Slack row: the extraction verdict, KindNew before any.
const (
	KindNew           Kind = "new"
	KindWaitsOnViewer Kind = "waits_on_viewer"
	KindWaitsOnOthers Kind = "waits_on_others"
	KindNeedsClosure  Kind = "needs_closure"
	KindResolved      Kind = "resolved"
	KindFYI           Kind = "fyi"
)

var kinds = []struct {
	kind  Kind
	label string
}{
	{KindWaitsOnViewer, "waits on you"},
	{KindNeedsClosure, "needs closure"},
	{KindNew, "not read yet"},
	{KindWaitsOnOthers, "waits on others"},
	{KindFYI, "fyi"},
	{KindResolved, "resolved"},
}

func kindLabel(k Kind) string {
	for _, x := range kinds {
		if x.kind == k {
			return x.label
		}
	}
	return string(k)
}

// ViewSlack lists the Slack units not closed.
const ViewSlack = "slack"

// FacetConversation groups rows by channel or DM.
const FacetConversation = "conversation"

// FacetChannel is a channel thread's channel name, without "#": the
// qualifier channel:<name>.
const FacetChannel = "channel"

// FacetWaitsOn is the core facet of whom a row waits on.
const FacetWaitsOn = "waits-on"

func derive(in *Input) Row {
	in = withGroupNames(in)
	pl := payload(in)
	open := openItems(in.items)
	kind := KindNew
	if in.hasState && in.state.Verdict != "" {
		kind = Kind(in.state.Verdict)
	}
	r := Row{
		Unit:   in.key.Unit(),
		Link:   link(in),
		Label:  label(pl),
		Title:  title(in, pl),
		Kind:   kind,
		Views:  []string{ViewSlack},
		Facets: map[string][]string{FacetConversation: {label(pl)}},
	}
	r.Summary = Summary{
		WaitsOn:   waitsOn(in, open, kind),
		Since:     since(open, pl.LastActivity),
		Closed:    kind == KindResolved && len(open) == 0,
		OpenItems: len(open),
		Reason:    reason(in),
		Line:      line(kind, len(open)),
	}
	for _, it := range open {
		r.Items = append(r.Items, RowItem{ID: it.ID, Kind: string(it.Kind), Summary: it.Summary, State: string(it.State),
			WaitingOn: names(it.WaitingOn), Since: itemSince(it)})
	}
	if w := waitingWho(r.Summary.WaitsOn); len(w) > 0 {
		r.Facets[FacetWaitsOn] = w
	}
	if pl.Channel != "" {
		r.Facets[FacetChannel] = []string{pl.Channel}
	}
	r.Payload, _ = json.Marshal(pl)
	return r
}

// waitingWho are the people and groups a row waits on, as its summary names them.
func waitingWho(summary string) []string {
	var out []string
	for w := range strings.SplitSeq(summary, ", ") {
		if w != "" && w != "nobody" {
			out = append(out, w)
		}
	}
	return out
}

func payload(in *Input) Payload {
	pl := Payload{Conversation: in.key.Conversation, Viewer: in.viewer, Topic: in.state.Topic, Verdict: in.state.Verdict,
		StartedByViewer: in.state.StartedByViewer, ClosurePosted: in.state.ClosurePosted || in.state.ClosureMarked,
		Followed: slices.Contains(in.object.Reasons, string(dslack.ReasonUserFollow))}
	if !in.hasState {
		pl.StartedByViewer = in.key.Segment == "" && len(in.msgs) > 0 && in.msgs[0].TS == in.key.TS &&
			in.viewer != "" && in.msgs[0].User == in.viewer
	}
	if dslack.ConversationKind(in.conv.Kind).DM() || in.key.Segment != "" {
		pl.DM, pl.With = true, with(in)
	} else {
		pl.Channel = in.conv.Name
	}
	if n := len(in.msgs); n > 1 {
		pl.Replies = n - 1
	}
	pl.LastActivity = in.object.LastActivityAt
	if n := len(in.msgs); n > 0 {
		pl.LastActivity = dslack.TSTime(in.msgs[n-1].TS)
	}
	for i := len(in.msgs) - 1; i >= 0; i-- {
		m := in.msgs[i]
		if !human(m, in.users) || m.User == in.viewer {
			continue
		}
		pl.LastHuman = &LastHuman{Who: person(m.User, in.users), What: firstLine(plain(m.Text, in.users), TitleRunes),
			At: dslack.TSTime(m.TS), Link: messageLink(in, m)}
		break
	}
	for _, it := range openItems(in.items) {
		for _, p := range it.WaitingOn {
			if !slices.ContainsFunc(pl.WaitsOn, func(q items.Person) bool { return q.Key == p.Key }) {
				pl.WaitsOn = append(pl.WaitsOn, p)
			}
		}
	}
	return pl
}

// with are the people of a DM besides the viewer: an im's other party, else
// the authors of its messages.
func with(in *Input) []string {
	if in.conv.DMUser != "" {
		return []string{displayName(in.conv.DMUser, in.users)}
	}
	var out []string
	for _, m := range in.msgs {
		if m.User == "" || m.User == in.viewer || !human(m, in.users) {
			continue
		}
		if n := displayName(m.User, in.users); !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func label(pl Payload) string {
	switch {
	case pl.DM && len(pl.With) > 0:
		return "DM with " + strings.Join(pl.With, ", ")
	case pl.DM:
		return "DM " + pl.Conversation
	case pl.Channel != "":
		return "#" + pl.Channel
	}
	return pl.Conversation
}

// title is a thread's first line, or "DM with <name>: <topic>" for a DM segment.
func title(in *Input, pl Payload) string {
	if in.key.Segment != "" {
		if pl.Topic == "" {
			return label(pl)
		}
		return label(pl) + ": " + pl.Topic
	}
	for _, m := range in.msgs {
		if m.TS != in.key.TS {
			continue
		}
		if t := firstLine(plain(m.Text, in.users), TitleRunes); t != "" {
			return t
		}
		for _, f := range m.Files {
			if f.Title != "" {
				return cut(f.Title, TitleRunes)
			}
		}
	}
	if len(in.msgs) > 0 {
		if t := firstLine(plain(in.msgs[0].Text, in.users), TitleRunes); t != "" {
			return t
		}
	}
	return "Slack thread in " + label(pl)
}

func link(in *Input) string {
	for _, m := range in.msgs {
		if m.Permalink != "" && (in.key.Segment != "" || m.TS == in.key.TS) {
			return m.Permalink
		}
	}
	ts := in.key.TS
	if in.key.Segment != "" {
		ts = ""
		if len(in.msgs) > 0 {
			ts = in.msgs[0].TS
		}
	}
	if p := permalink(in.host, in.key.Conversation, ts, ""); p != "" {
		return p
	}
	team := in.key.Team
	if in.key.Segment != "" || len(in.msgs) == 0 {
		return "https://app.slack.com/client/" + team + "/" + in.key.Conversation
	}
	return "https://app.slack.com/client/" + team + "/" + in.key.Conversation + "/thread/" + in.key.Conversation + "-" + in.key.TS
}

// messageLink is m's permalink, else one built on the workspace host.
func messageLink(in *Input, m store.MessageRow) string {
	if m.Permalink != "" {
		return m.Permalink
	}
	return permalink(in.host, in.key.Conversation, m.TS, m.ThreadTS)
}

// permalink is the link Slack hands to its desktop app: the message at ts
// in conv on host, in its thread when threadTS names another message; ""
// when host or ts is unknown.
func permalink(host, conv, ts, threadTS string) string {
	if host == "" || ts == "" {
		return ""
	}
	out := "https://" + host + "/archives/" + conv + "/p" + strings.Replace(ts, ".", "", 1)
	if threadTS != "" && threadTS != ts {
		out += "?thread_ts=" + threadTS + "&cid=" + conv
	}
	return out
}

// slackHost is the host of a Slack permalink, "" for anything else.
func slackHost(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	if !strings.HasSuffix(h, ".slack.com") || h == "app.slack.com" {
		return ""
	}
	return h
}

func reason(in *Input) string {
	if in.hasState && in.state.VerdictWhy != "" {
		return in.state.VerdictWhy
	}
	if !in.hasState {
		return "not read yet"
	}
	return kindLabel(Kind(in.state.Verdict))
}

func line(k Kind, open int) string {
	switch open {
	case 0:
		return kindLabel(k)
	case 1:
		return kindLabel(k) + " · 1 open item"
	}
	return kindLabel(k) + " · " + strconv.Itoa(open) + " open items"
}

func waitsOn(in *Input, open []items.Item, k Kind) string {
	me := "slack:" + in.viewer
	var others []string
	for _, it := range open {
		for _, p := range it.WaitingOn {
			if in.viewer != "" && p.Key == me {
				return "me"
			}
			if n := personName(p); !slices.Contains(others, n) {
				others = append(others, n)
			}
		}
	}
	switch {
	case k == KindWaitsOnViewer:
		return "me"
	case len(others) > 0:
		return strings.Join(others, ", ")
	}
	return "nobody"
}

func since(open []items.Item, last time.Time) time.Time {
	var out time.Time
	for _, it := range open {
		if s := itemSince(it); !s.IsZero() && (out.IsZero() || s.Before(out)) {
			out = s
		}
	}
	if out.IsZero() {
		return last
	}
	return out
}

func itemSince(it items.Item) time.Time {
	if !it.Origin.At.IsZero() {
		return it.Origin.At
	}
	return it.CreatedAt
}

func openItems(its []items.Item) []items.Item {
	var out []items.Item
	for _, it := range its {
		if it.State == items.Open {
			out = append(out, it)
		}
	}
	return out
}

func names(ps []items.Person) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = personName(p)
	}
	return out
}

var groupID = regexp.MustCompile(`^S[A-Z0-9]+$`)

// withGroupNames is in with each user group an item names by its raw id
// named by its handle in the messages, else as dslack.UnknownGroup.
func withGroupNames(in *Input) *Input {
	var handles map[string]string
	var out []items.Item
	for i, it := range in.items {
		for j, p := range it.WaitingOn {
			id, ok := strings.CutPrefix(p.Key, "group:")
			if !ok || !groupID.MatchString(id) || (p.Name != id && p.Name != "") {
				continue
			}
			if handles == nil {
				handles = map[string]string{}
				for _, m := range in.msgs {
					dslack.GroupHandles(handles, m.Text)
				}
				out = slices.Clone(in.items)
			}
			if &out[i].WaitingOn[0] == &in.items[i].WaitingOn[0] {
				out[i].WaitingOn = slices.Clone(it.WaitingOn)
			}
			out[i].WaitingOn[j].Name = dslack.GroupName(id, handles)
		}
	}
	if out == nil {
		return in
	}
	c := *in
	c.items = out
	return &c
}

func personName(p items.Person) string {
	if p.Name != "" {
		return p.Name
	}
	return p.Key
}

// human says whether a person wrote m: a user who is not a bot, whatever
// app posted it for them.
func human(m store.MessageRow, users map[string]store.UserRow) bool {
	return m.User != "" && m.User != "USLACKBOT" && !users[m.User].IsBot && !m.Deleted &&
		!slices.Contains(dslack.HiddenSubtypes, m.Subtype)
}

func person(id string, users map[string]store.UserRow) items.Person {
	p := items.Person{Key: "slack:" + id, Name: displayName(id, users)}
	if e := users[id].Email; e != "" {
		p.Aliases = []string{"email:" + e}
	}
	return p
}

func displayName(id string, users map[string]store.UserRow) string {
	if n := nameOf(id, users); n != "" {
		return n
	}
	return id
}
