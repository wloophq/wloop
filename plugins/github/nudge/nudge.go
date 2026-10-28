// Package nudge is the GitHub plugin's nudge: whom a nudge asks on a pull
// request, when it refuses, and the nudge drafts its github-nudge rule
// proposes on stalled pull requests.
package nudge

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"text/template"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/row"
	"github.com/wloophq/wloop/plugins/github/situation"
)

// RenudgeAfter is how long a wait that a nudge did not move stays nudged.
const RenudgeAfter = 24 * time.Hour

// Maintainers is whom a nudge asks on the user's own pull request with no
// reviewer: the skill mentions nobody, or the one code owner.
const Maintainers = "the maintainers (no reviewer requested)"

// Rule is the Origin.Rule of the nudge drafts the plugin proposes.
const Rule = "github-nudge"

// Purpose is the purpose of the plugin's nudge drafts.
const Purpose = "nudge"

// The effects a proposed nudge draft declares, as the manifests name them.
const (
	EffectNudged     = "pr.nudged"
	EffectTaskNudged = "task.nudged"
)

// The host overrides a proposal reads, as RoundState names them.
const (
	overrideMutedUntil           = "muted_until"
	overrideMuteUntilStateChange = "mute_until_state_change"
	overrideMutedKind            = "muted_kind"
	overrideMutedWaitingOn       = "muted_waiting_on"
	overrideHold                 = "hold"
	overrideTrue                 = "true"
)

// The roles plugin.toml declares that drive a pull request's row.
const (
	roleReview  = "pr.review"
	roleAddress = "pr.address"
)

// Situation is what a pull request waits on, as a nudge reads it.
type Situation struct {
	Kind    string
	WaitsOn string
	Since   time.Time
	Reason  string
}

// Of is r's source situation.
func Of(r row.Row) Situation {
	s := r.Situation
	return Situation{Kind: string(s.Kind), WaitsOn: s.WaitingOn, Since: s.Since, Reason: s.Reason}
}

// Targets is who a nudge mentions, or why it mentions nobody: s's kind,
// waits-on and reason, author its pull request's author.
func Targets(s Situation, author, viewer string, ignore []string) ([]string, string) {
	switch situation.Kind(s.Kind) {
	case situation.KindDone:
		return nil, "is " + s.Reason
	case situation.KindLoopWorking, situation.KindLoopWaiting, situation.KindLoopStopped, situation.KindHeldByCap:
		return nil, "waits on the review loop (" + s.Reason + ")"
	case situation.KindNoReviewer:
		if viewer != "" && strings.EqualFold(author, viewer) {
			return []string{Maintainers}, ""
		}
	}
	var people, teams, skipped []string
	ghost := false
	for who := range strings.SplitSeq(s.WaitsOn, ", ") {
		if who == "author" {
			if author == "" {
				ghost = true
				continue
			}
			who = author
		}
		switch {
		case who == "" || who == "me" || who == "nobody" || strings.EqualFold(who, viewer):
			continue
		case strings.HasSuffix(who, "[bot]"):
			skipped = append(skipped, who+", a bot")
			continue
		case slices.ContainsFunc(ignore, func(x string) bool { return strings.EqualFold(x, who) }):
			skipped = append(skipped, who+", which automated_reviewers or address_skip_reviewers lists")
			continue
		}
		if team, ok := strings.CutPrefix(who, "team:"); ok {
			teams = append(teams, "@"+team)
			continue
		}
		people = append(people, "@"+who)
	}
	// A team is mentioned only when no person is requested: mentioning it
	// notifies every member.
	switch {
	case len(people) > 0:
		return people, ""
	case len(teams) > 0:
		return teams, ""
	case len(skipped) > 0:
		return nil, "waits on " + strings.Join(skipped, " and ")
	case ghost:
		return nil, "waits on an author GitHub no longer knows"
	}
	return nil, "waits on you (" + s.Reason + ")"
}

// Refusal is the text a nudge is refused with now, "" when it would go;
// unstarted are the turns that ended before their agent started them,
// whose nudged events do not count.
func Refusal(ref pr.Ref, who []string, why string, since time.Time, evs []pr.Event, unstarted map[string]bool, now time.Time) string {
	if len(who) == 0 {
		return fmt.Sprintf("%s %s; a nudge asks someone else for the next step", ref, why)
	}
	for _, e := range slices.Backward(evs) {
		if again := e.At.Add(RenudgeAfter); e.Kind == pr.KindNudged && !e.At.Before(since) && now.Before(again) && !unstarted[e.Key] {
			const stamp = "2006-01-02 15:04"
			return fmt.Sprintf("%s: already nudged %s at %s; again after %s", ref, e.Data, e.At.Local().Format(stamp), again.Local().Format(stamp))
		}
	}
	return ""
}

// Unstarted are the ids of b's turns that ended before their agent
// started them, which posted nothing.
func Unstarted(b src.Book) map[string]bool {
	var out map[string]bool
	for _, t := range b.Turns {
		if !open(t.State) && t.StartedAt.IsZero() {
			if out == nil {
				out = map[string]bool{}
			}
			out[t.ID] = true
		}
	}
	return out
}

func open(state string) bool { return state == "sent" || state == "started" || state == "asked" }

// Running says whether b's turn that drives the row sets its situation:
// the loop works on the pull request, asks or stopped, so the pull request
// waits on the loop.
func Running(b src.Book) bool {
	var driving *src.Turn
	for i, t := range b.Turns {
		if (t.Role == roleReview || t.Role == roleAddress) && (driving == nil || t.Seq > driving.Seq) {
			driving = &b.Turns[i]
		}
	}
	if driving == nil {
		return false
	}
	switch driving.State {
	case "asked", "failed", "unrecorded", "started":
		return true
	}
	return false
}

// Muted says whether the host's overrides o mute a pull request whose
// situation is s at now.
func Muted(o map[string]string, s Situation, now time.Time) bool {
	if situation.Kind(s.Kind) == situation.KindDone {
		return false
	}
	if raw := o[overrideMutedUntil]; raw != "" {
		if until, err := time.Parse(time.RFC3339Nano, raw); err == nil && now.Before(until) {
			return true
		}
	}
	return o[overrideMuteUntilStateChange] == overrideTrue && s.Kind == o[overrideMutedKind] && sameWaitingOn(s.WaitsOn, o[overrideMutedWaitingOn])
}

func sameWaitingOn(a, b string) bool {
	set := func(s string) []string {
		v := strings.Split(s, ", ")
		slices.Sort(v)
		return v
	}
	return slices.Equal(set(a), set(b))
}

// Host is what a proposal reads of the host for one pull request: its
// overrides and its turns.
type Host struct {
	Overrides map[string]string
	Book      src.Book
}

// Stalled says whether r is one the github-nudge rule considers: stalled
// in scope, and not one its card alone shows (a draft, approved by the
// viewer, done).
func Stalled(r row.Row) bool {
	switch r.Kind() {
	case situation.KindDraft, situation.KindDone:
		return false
	}
	return r.Stalled() && len(r.Payload.Reasons) > 0 && !r.Payload.Approved
}

// Propose is the nudge draft of r, whom it asks, when r is stalled, the
// host holds no round, mute or loop turn on it, and the nudge would not be
// refused now; its text is x's.
func Propose(r row.Row, h Host, x Texts, ignore []string, now time.Time) (src.Draft, bool, error) {
	s := Of(r)
	if !Stalled(r) || h.Overrides[overrideHold] == overrideTrue || Running(h.Book) || Muted(h.Overrides, s, now) {
		return src.Draft{}, false, nil
	}
	who, why := Targets(s, r.Payload.Author, r.Payload.Viewer, ignore)
	if Refusal(r.PR.Ref, who, why, s.Since, r.Events(), Unstarted(h.Book), now) != "" {
		return src.Draft{}, false, nil
	}
	text, err := x.Text(who, s)
	if err != nil {
		return src.Draft{}, false, err
	}
	u := r.PR.Ref.Unit()
	return src.Draft{
		Unit:    u,
		Items:   waitingItems(r, who),
		Purpose: Purpose,
		Effects: []string{EffectNudged, EffectTaskNudged},
		Text:    text,
		Origin:  src.DraftOrigin{Kind: originAuto, Rule: Rule, Key: u.Provider + ":" + u.Key},
	}, true, nil
}

// originAuto is the origin kind of a draft a plugin's rule proposes.
const originAuto = "auto"

// Reader is what Proposals reads of the host.
type Reader interface {
	Books(ctx context.Context, units []sdk.Unit) ([]src.Book, error)
	RoundState(ctx context.Context, u sdk.Unit) (src.RoundState, error)
}

// Proposals are the nudge drafts of rows at now, reading the host's
// overrides and turns of the stalled ones only.
func Proposals(ctx context.Context, rows []row.Row, rd Reader, x Texts, ignore []string, now time.Time) ([]src.Draft, error) {
	var candidates []row.Row
	var units []sdk.Unit
	for _, r := range rows {
		if Stalled(r) && r.Payload.Viewer != "" {
			candidates = append(candidates, r)
			units = append(units, r.PR.Ref.Unit())
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	books := map[sdk.Unit]src.Book{}
	bs, err := rd.Books(ctx, units)
	if err != nil && !errors.Is(err, src.ErrUnsupported) {
		return nil, err
	}
	for _, b := range bs {
		books[b.Unit] = b
	}
	var out []src.Draft
	for _, r := range candidates {
		u := r.PR.Ref.Unit()
		st, err := rd.RoundState(ctx, u)
		if err != nil {
			return nil, fmt.Errorf("the round state of %s: %w", r.PR.Ref, err)
		}
		d, ok, err := Propose(r, Host{Overrides: st.Overrides, Book: books[u]}, x, ignore, now)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, d)
		}
	}
	return out, nil
}

// TextsFile is the nudge's texts in the plugin's profile layer.
const TextsFile = "prompts/github-nudge.tmpl"

// Texts are the nudge's texts, as the profile's templates word them: the
// draft of the github-nudge rule and the situation line of the prepare.
type Texts struct{ t *template.Template }

// ParseTexts reads raw, the profile's TextsFile.
func ParseTexts(raw string) (Texts, error) {
	t, err := template.New(TextsFile).Option("missingkey=error").Parse(raw)
	if err != nil {
		return Texts{}, fmt.Errorf("%s: %w", TextsFile, err)
	}
	for _, name := range []string{"draft", "waiting", "label"} {
		if t.Lookup(name) == nil {
			return Texts{}, fmt.Errorf("%s defines no %q template", TextsFile, name)
		}
	}
	return Texts{t: t}, nil
}

func (x Texts) render(name string, data any) (string, error) {
	var b strings.Builder
	if err := x.t.ExecuteTemplate(&b, name, data); err != nil {
		return "", fmt.Errorf("%s: %w", TextsFile, err)
	}
	return strings.TrimSpace(b.String()), nil
}

// Text asks who for the next step; a person is a mention, a team is
// written as GitHub mentions it, since its key would post as plain text.
func (x Texts) Text(who []string, s Situation) (string, error) {
	since := ""
	if !s.Since.IsZero() {
		since = s.Since.UTC().Format(time.DateOnly)
	}
	mentions := make([]string, len(who))
	for i, w := range who {
		login := strings.TrimPrefix(w, "@")
		if strings.Contains(login, "/") {
			mentions[i] = w
			continue
		}
		mentions[i] = "@{github:" + login + "}"
	}
	return x.render("draft", map[string]any{"Mentions": strings.Join(mentions, " "), "Since": since, "Kind": s.Kind,
		"Maintainers": slices.Equal(who, []string{Maintainers})})
}

// Waiting is the line the nudge intent's prompt reads of whom the pull
// request waits on and since when, worded by the situation's kind: a
// reason can carry CI job names.
func (x Texts) Waiting(who []string, s Situation) (string, error) {
	since := ""
	if !s.Since.IsZero() {
		since = s.Since.Local().Format(time.RFC3339)
	}
	return x.render("waiting", map[string]any{"Who": strings.Join(who, ", "), "Since": since, "Kind": s.Kind})
}

// waitingItems are r's open items waiting on someone the nudge asks.
func waitingItems(r row.Row, who []string) []string {
	keys := make([]string, 0, len(who))
	for _, w := range who {
		login := strings.TrimPrefix(w, "@")
		if strings.Contains(login, "/") {
			keys = append(keys, "group:"+login)
		} else {
			keys = append(keys, pr.Provider+":"+login)
		}
	}
	var out []string
	for _, it := range r.Items() {
		if it.State == "open" && slices.ContainsFunc(it.WaitingOn, func(x src.Person) bool {
			return slices.ContainsFunc(keys, func(k string) bool { return strings.EqualFold(k, x.Key) })
		}) {
			out = append(out, it.ID)
		}
	}
	return out
}

var loginMentionRE = regexp.MustCompile("(?:^|[^\\w`])@([A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:/[A-Za-z0-9._-]+)?)")

// Who is whom body asks, as the nudged event records it.
func Who(body string) string {
	var who []string
	for _, m := range loginMentionRE.FindAllStringSubmatch(body, -1) {
		if w := "@" + m[1]; !slices.ContainsFunc(who, func(s string) bool { return strings.EqualFold(s, w) }) {
			who = append(who, w)
		}
	}
	if len(who) == 0 {
		return Maintainers
	}
	return strings.Join(who, ", ")
}
