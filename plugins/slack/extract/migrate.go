package extract

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	slack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// personKeysMigration gives the ledger's people the keys and names the plugin writes today.
// Versions up to v0.2.0 keyed a person the model named by anything but a bare id as a team
// ("group:" + the text), so a user the ledger rendered as "ana (U0000000001)" and echoed
// back came to be keyed "group:ana (U0000000001)", then "group:group:..." on each pass, and
// a start-time repair on a development build left people named by their id.
const personKeysMigration = "person-keys"

// migrate applies, once per install, the ledger migrations not yet recorded, at the first
// pass after the process started.
func (j *Job) migrate(ctx context.Context, now time.Time) error {
	if j.migrated {
		return nil
	}
	done, err := j.Store.DB.Migrated(ctx, personKeysMigration)
	if err != nil {
		return err
	}
	if done {
		j.migrated = true
		return nil
	}
	all, err := j.Ledger.All(ctx)
	if err != nil {
		return fmt.Errorf("slack extraction: %s migration: %w", personKeysMigration, err)
	}
	var its []items.Item
	for _, u := range slices.SortedFunc(maps.Keys(all), unit.Compare) {
		its = append(its, all[u]...)
	}
	ids := map[string]bool{}
	for _, it := range its {
		for _, p := range peopleOf(it) {
			if id, ok := legacyID(p.Key); ok && slack.IsUserID(id) {
				ids[id] = true
			}
		}
	}
	users, err := j.Facts.Users(ctx, slices.Sorted(maps.Keys(ids)))
	if err != nil {
		return err
	}
	var ops []items.Op
	skipped := 0
	for _, it := range its {
		o := migrationOps(it, users)
		if len(o) > 0 && it.Confirmed {
			skipped++
			continue
		}
		ops = append(ops, o...)
	}
	if len(ops) > 0 {
		res, err := j.Ledger.Apply(ctx, items.Extracted, ops)
		if err != nil {
			return fmt.Errorf("slack extraction: %s migration: %w", personKeysMigration, err)
		}
		j.logf("slack extraction: %s migration: %d ops applied, %d refused, %d confirmed items left as they are",
			personKeysMigration, len(ops)-len(res.Refused), len(res.Refused), skipped)
	}
	if err := j.Store.DB.MarkMigrated(ctx, personKeysMigration, now); err != nil {
		return err
	}
	j.migrated = true
	return nil
}

// peopleOf are the people it names, in field order.
func peopleOf(it items.Item) []items.Person {
	ps := []items.Person{}
	if it.Requester.Key != "" {
		ps = append(ps, it.Requester)
	}
	ps = append(ps, it.WaitingOn...)
	ps = append(ps, it.Assignees...)
	for _, o := range it.Options {
		ps = append(ps, o.ArguedBy...)
	}
	return ps
}

// migrationOps are the ops that give it's people today's keys and names; none when every
// person already has them.
func migrationOps(it items.Item, users map[string]slack.User) []items.Op {
	b := opBuilder{in: Input{Unit: it.Unit, Users: users}}
	fix := func(ps []items.Person) ([]items.Person, bool) {
		out, changed := make([]items.Person, len(ps)), false
		for i, p := range ps {
			out[i] = b.person(legacyPersonOut(p))
			if out[i].Key == "" {
				out[i] = p
			}
			changed = changed || out[i].Key != p.Key || out[i].Name != p.Name || !slices.Equal(out[i].Aliases, p.Aliases)
		}
		return out, changed
	}
	anchor := it.AssignedAt
	if anchor.Ref == "" {
		anchor = it.Origin
	}
	anchor.Unit = it.Unit
	var ops []items.Op
	var f items.Fields
	changed := false
	if it.Requester.Key != "" {
		if ps, ok := fix([]items.Person{it.Requester}); ok {
			f.Requester, changed = &ps[0], true
		}
	}
	if ps, ok := fix(it.WaitingOn); ok {
		f.WaitingOn, changed = &ps, true
	}
	opts := slices.Clone(it.Options)
	for i, o := range opts {
		if ps, ok := fix(o.ArguedBy); ok {
			opts[i].ArguedBy, f.Options, changed = ps, &opts, true
		}
	}
	if changed {
		ops = append(ops, items.Op{Op: items.OpUpdate, Unit: it.Unit, Item: it.ID, Anchor: anchor, Fields: &f})
	}
	if ps, ok := fix(it.Assignees); ok {
		ops = append(ops, items.Op{Op: items.OpAssign, Unit: it.Unit, Item: it.ID, Anchor: anchor, Assignees: ps, Basis: it.Basis})
	}
	return ops
}

var (
	legacyMention   = regexp.MustCompile(`<@([UW][A-Z0-9]{8,})(?:\|[^>]*)?>`)
	legacyBracket   = regexp.MustCompile(`\(([UWS][A-Z0-9]{8,})\)`)
	legacyLeadingID = regexp.MustCompile(`^([UWS][A-Z0-9]{8,})\b`)
	legacyNoise     = regexp.MustCompile(`[<>\n]+`)
)

// legacyID is the Slack id a key an earlier version wrote names: a bare id, a mention, an
// id in brackets after a name, or an id a description follows; false for a team's name.
func legacyID(key string) (string, bool) {
	text := strings.TrimSpace(key)
	if id, ok := strings.CutPrefix(text, slack.UserKey("")); ok {
		return id, slack.IsUserID(id)
	}
	for {
		rest, ok := strings.CutPrefix(text, slack.GroupKey(""))
		if !ok {
			break
		}
		text = strings.TrimSpace(rest)
	}
	if slack.IsUserID(text) || slack.IsGroupID(text) {
		return text, true
	}
	for _, re := range []*regexp.Regexp{legacyMention, legacyBracket, legacyLeadingID} {
		if m := re.FindStringSubmatch(text); m != nil {
			return m[1], true
		}
	}
	return "", false
}

// legacyPersonOut reads p as an answer would name them today: by id when the key carries
// one, else as a team by the name the key or the name holds, cleaned of the prefixes and
// mentions earlier versions let through.
func legacyPersonOut(p items.Person) PersonOut {
	if id, ok := legacyID(p.Key); ok {
		return PersonOut{ID: id, Name: legacyName(p.Name, id)}
	}
	text := p.Key
	for {
		rest, ok := strings.CutPrefix(text, slack.GroupKey(""))
		if !ok {
			break
		}
		text = strings.TrimSpace(rest)
	}
	return PersonOut{Name: strings.TrimSpace(legacyNoise.ReplaceAllString(text, " "))}
}

// legacyName is the name worth keeping of a person keyed id: name with the prefixes, the
// bracketed id and mentions earlier versions wrote removed; "" when nothing but the id is left.
func legacyName(name, id string) string {
	text := strings.TrimSpace(name)
	for {
		rest, ok := strings.CutPrefix(text, slack.GroupKey(""))
		if !ok {
			break
		}
		text = strings.TrimSpace(rest)
	}
	if m := legacyMention.FindStringSubmatch(text); m != nil && strings.Contains(text, "|") {
		text = strings.TrimSuffix(strings.SplitN(text, "|", 2)[1], ">")
	}
	text = legacyBracket.ReplaceAllString(text, "")
	text = strings.TrimSpace(legacyNoise.ReplaceAllString(text, " "))
	if text == id || text == slack.UnknownGroup || strings.HasPrefix(text, id+" ") {
		return ""
	}
	return text
}
