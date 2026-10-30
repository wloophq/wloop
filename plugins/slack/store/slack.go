package store

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/plugins/slack/unit"
)

// SlackStore is the Slack source's tables. Only admitted objects have
// messages; a message outside them moves its conversation's hwm only.
type SlackStore struct{ db *DB }

func (db *DB) Slack() *SlackStore { return &SlackStore{db} }

// Object kinds of slack_objects.
const (
	SlackThread = "thread"
	SlackDM     = "dm"
)

// Change kinds of slack_changes.
const (
	SlackNew      = "new"
	SlackEdited   = "edited"
	SlackDeleted  = "deleted"
	SlackReaction = "reaction"
)

// SlackDiscoverCursor is the source's discovery cursor.
const SlackDiscoverCursor = "discover"

type ConversationRow struct {
	ID, Team, Name, Kind string
	IsMember, Archived   bool
	DMUser               string
	HWM                  string
	Admitted             bool
	UpdatedAt            time.Time
}

type ObjectRow struct {
	Object, Conversation, TS string
	Kind                     string // SlackThread or SlackDM
	Version, LatestReply     string
	ReplyCount               int
	Watched                  bool
	Reasons                  []string
	LastActivityAt           time.Time
	NextCheckAt              time.Time // zero: due now
	SeenAt, AdmittedAt       time.Time
}

type FileRow struct {
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	Mimetype string `json:"mimetype,omitempty"`
}

type ReactionRow struct {
	Name  string
	Users []string
}

type MessageRow struct {
	Conversation, TS, ThreadTS, ParentUser string
	User, BotID, AppID, Username           string
	Subtype, Text, EditedTS                string
	Deleted                                bool
	Files                                  []FileRow
	Links, Mentions                        []string
	// Reactions nil was not read and leaves the stored set; non-nil, even
	// empty, is the full set and replaces it.
	Reactions  []ReactionRow
	Permalink  string
	Via        string
	ReceivedAt time.Time
	// Units are what a change of this message notes; not stored.
	Units []unit.Unit
}

// Anchor is the ts that places the message: its thread's parent, or its own.
func (m MessageRow) Anchor() string { return cmp.Or(m.ThreadTS, m.TS) }

type ObjectFacts struct {
	Object   ObjectRow
	Messages []MessageRow
}

type FactsRows struct {
	Team          string
	Conversations []ConversationRow
	Objects       []ObjectFacts
	Users         []UserRow
	Cursor        string // "" keeps the stored one
}

type UserRow struct {
	ID, Name, DisplayName, Email string
	IsBot, Deleted               bool
	FetchedAt                    time.Time
}

type ChangeRow struct {
	Seq                        int64
	Conversation, TS, ThreadTS string
	Kind, EditedTS             string
	At                         time.Time
}

// SaveResult is what a Save changed: the units its notes named, and whether
// a changed message had no unit (a DM range not segmented yet).
type SaveResult struct {
	Units    []unit.Unit
	Unitless bool
	Changes  int // change rows written
}

// MessageQuery selects a conversation's messages by anchor: Thread names one
// thread, From and To an inclusive range (To "" open). After and Limit page.
// TS alone selects the one message at it.
type MessageQuery struct {
	TS       string
	Thread   string
	From, To string
	After    string
	Limit    int
	// Before keeps the messages anchored before it; Newest takes the Limit of the newest anchors, returned in ts order.
	Before string
	Newest bool
}

// slackTime makes a stored time sort as text: UTC, whole seconds.
func slackTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Truncate(time.Second).Format(TimeLayout)
}

func parseSlackTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(TimeLayout, s)
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	if string(b) == "null" {
		return "[]"
	}
	return string(b)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// compareSlackTS orders two Slack timestamps numerically; "" sorts first.
func compareSlackTS(a, b string) int {
	as, af, _ := strings.Cut(a, ".")
	bs, bf, _ := strings.Cut(b, ".")
	if c := cmp.Compare(len(as), len(bs)); c != 0 {
		return c
	}
	if c := cmp.Compare(as, bs); c != 0 {
		return c
	}
	n := max(len(af), len(bf))
	return cmp.Compare(af+strings.Repeat("0", n-len(af)), bf+strings.Repeat("0", n-len(bf)))
}

const (
	slackObjectColumns = `object, conversation, ts, kind, version, latest_reply, reply_count, watched, reasons,
		last_activity_at, next_check_at, seen_at, admitted_at`
	slackObjectSQL  = `SELECT ` + slackObjectColumns + ` FROM slack_objects WHERE object = ?`
	slackWatchedSQL = `SELECT ` + slackObjectColumns + ` FROM slack_objects
		WHERE watched = 1 AND next_check_at <= ? ORDER BY next_check_at, object`
	slackInsertObjectSQL = `INSERT INTO slack_objects (` + slackObjectColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, '', ?)
		ON CONFLICT (object) DO UPDATE SET version = excluded.version, latest_reply = excluded.latest_reply,
		reply_count = excluded.reply_count,
		last_activity_at = max(last_activity_at, excluded.last_activity_at),
		next_check_at = CASE WHEN excluded.next_check_at = '' THEN next_check_at ELSE excluded.next_check_at END,
		admitted_at = CASE WHEN admitted_at = '' THEN excluded.admitted_at ELSE admitted_at END`

	slackConversationColumns = `id, team, name, kind, is_member, archived, dm_user, hwm, admitted, updated_at`
	slackPutConversationSQL  = `INSERT INTO slack_conversations (id, team, name, kind, is_member, archived, dm_user, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET team = CASE WHEN excluded.team = '' THEN team ELSE excluded.team END,
		name = CASE WHEN excluded.name = '' THEN name ELSE excluded.name END, kind = excluded.kind, is_member = excluded.is_member, archived = excluded.archived,
		dm_user = excluded.dm_user, updated_at = excluded.updated_at`
	slackAdmitSQL = `INSERT INTO slack_conversations (id, team, admitted, hwm) VALUES (?, ?, 1, ?)
		ON CONFLICT (id) DO UPDATE SET admitted = 1, team = CASE WHEN team = '' THEN excluded.team ELSE team END`
	slackHWMSQL = `INSERT INTO slack_conversations (id, hwm) VALUES (?, ?)
		ON CONFLICT (id) DO UPDATE SET hwm = excluded.hwm`
	slackAdmittedSQL = `SELECT ` + slackConversationColumns + ` FROM slack_conversations WHERE admitted = 1 ORDER BY id`

	slackMessageColumns = `conversation, ts, thread_ts, parent_user, user, bot_id, app_id, username, subtype, text,
		edited_ts, deleted, files, links, mentions, permalink, via, received_at`
	slackMessageSQL       = `SELECT ` + slackMessageColumns + ` FROM slack_messages WHERE conversation = ? AND ts = ?`
	slackInsertMessageSQL = `INSERT INTO slack_messages (` + slackMessageColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	slackAnchor           = `(CASE WHEN thread_ts = '' THEN ts ELSE thread_ts END)`
	slackMessagesOfQuery  = `SELECT ` + slackMessageColumns + ` FROM slack_messages WHERE conversation = ?`
	slackMessagesByAnchor = `SELECT ` + slackMessageColumns + ` FROM slack_messages INDEXED BY slack_messages_anchor WHERE conversation = ?`
	slackReactionsSQL     = `SELECT name, user FROM slack_reactions WHERE conversation = ? AND ts = ? ORDER BY name, user`
	slackReactionsOfSQL   = `SELECT ts, name, user FROM slack_reactions
		WHERE conversation = ? AND ts IN (SELECT value FROM json_each(?)) ORDER BY ts, name, user`

	slackInsertChangeSQL = `INSERT OR IGNORE INTO slack_changes (conversation, ts, thread_ts, kind, edited_ts, at) VALUES (?, ?, ?, ?, ?, ?)`
	slackChangesSQL      = `SELECT seq, conversation, ts, thread_ts, kind, edited_ts, at FROM slack_changes WHERE seq > ? ORDER BY seq LIMIT ?`

	slackUserColumns = `id, name, display_name, email, is_bot, deleted, fetched_at`
	slackUserByEmail = `SELECT ` + slackUserColumns + ` FROM slack_users WHERE email = ? COLLATE NOCASE ORDER BY deleted, id LIMIT 1`
	slackPutUserSQL  = `INSERT INTO slack_users (` + slackUserColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, display_name = excluded.display_name, email = excluded.email,
		is_bot = excluded.is_bot, deleted = excluded.deleted, fetched_at = excluded.fetched_at`

	slackCursorSQL    = `SELECT value FROM slack_cursors WHERE name = ?`
	slackSetCursorSQL = `INSERT INTO slack_cursors (name, value, at) VALUES (?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET value = excluded.value, at = excluded.at WHERE value != excluded.value`
)

// Save writes the facts in one transaction and notes TopicSourceEvents with
// the units of every message that changed: new, edited, deleted or with
// other reactions. A message stored again unchanged writes and notes nothing.
func (s *SlackStore) Save(ctx context.Context, f FactsRows, now time.Time) (SaveResult, error) {
	var res SaveResult
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		res = SaveResult{}
		if err := putConversations(ctx, tx, f.Conversations, now); err != nil {
			return err
		}
		if err := putUsers(ctx, tx, f.Users); err != nil {
			return err
		}
		hwm := map[string]string{}
		for _, o := range f.Objects {
			if err := saveObject(ctx, tx, f.Team, o.Object, now); err != nil {
				return err
			}
			for _, m := range o.Messages {
				m.Conversation = cmp.Or(m.Conversation, o.Object.Conversation)
				if err := inside(o.Object, m); err != nil {
					return err
				}
				changed, rows, err := saveMessage(ctx, tx, m, now)
				if err != nil {
					return fmt.Errorf("slack message %s/%s: %w", m.Conversation, m.TS, err)
				}
				res.Changes += rows
				if changed {
					res.Units = append(res.Units, m.Units...)
					res.Unitless = res.Unitless || len(m.Units) == 0
				}
				if compareSlackTS(m.TS, hwm[m.Conversation]) > 0 {
					hwm[m.Conversation] = m.TS
				}
			}
		}
		for conv, ts := range hwm {
			if err := moveHWM(ctx, tx, conv, ts); err != nil {
				return err
			}
		}
		if f.Cursor != "" {
			if _, err := tx.ExecContext(ctx, slackSetCursorSQL, SlackDiscoverCursor, f.Cursor, slackTime(now)); err != nil {
				return err
			}
		}
		slices.SortFunc(res.Units, compareUnit)
		res.Units = slices.Compact(res.Units)
		if len(res.Units) > 0 {
			tx.Touch(res.Units...)
		}
		if res.Unitless {
			tx.Touch()
		}
		return nil
	})
	return res, err
}

func compareUnit(a, b unit.Unit) int {
	switch {
	case a.Less(b):
		return -1
	case b.Less(a):
		return 1
	}
	return 0
}

// inside refuses a message outside its object: another conversation, or in
// a thread object another anchor.
func inside(o ObjectRow, m MessageRow) error {
	if m.Conversation != o.Conversation {
		return fmt.Errorf("slack message %s/%s is not in the conversation of object %s", m.Conversation, m.TS, o.Object)
	}
	if cmp.Or(o.Kind, SlackThread) == SlackThread && m.Anchor() != o.TS {
		return fmt.Errorf("slack message %s/%s is not in thread object %s", m.Conversation, m.TS, o.Object)
	}
	return nil
}

func saveObject(ctx context.Context, tx *Tx, team string, o ObjectRow, now time.Time) error {
	if o.Object == "" || o.Conversation == "" {
		return fmt.Errorf("slack object %q: no id or conversation", o.Object)
	}
	kind := cmp.Or(o.Kind, SlackThread)
	if _, err := tx.ExecContext(ctx, slackInsertObjectSQL, o.Object, o.Conversation, o.TS, kind, o.Version, o.LatestReply,
		o.ReplyCount, jsonText([]string{"admission"}), slackTime(o.LastActivityAt), slackTime(o.NextCheckAt), slackTime(now)); err != nil {
		return fmt.Errorf("slack object %s: %w", o.Object, err)
	}
	_, err := tx.ExecContext(ctx, slackAdmitSQL, o.Conversation, team, "")
	return err
}

func moveHWM(ctx context.Context, tx *Tx, conv, ts string) error {
	var cur string
	err := tx.QueryRowContext(ctx, `SELECT hwm FROM slack_conversations WHERE id = ?`, conv).Scan(&cur)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if compareSlackTS(ts, cur) <= 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, slackHWMSQL, conv, ts)
	return err
}

// saveMessage writes m against the stored row and its change row; changed
// says whether the stored state moved, rows how many change rows were new.
func saveMessage(ctx context.Context, tx *Tx, m MessageRow, now time.Time) (changed bool, rows int, err error) {
	if m.Conversation == "" || m.TS == "" {
		return false, 0, errors.New("no conversation or ts")
	}
	if m.ThreadTS == m.TS {
		m.ThreadTS = ""
	}
	cur, ok, err := scanMessage(tx.QueryRowContext(ctx, slackMessageSQL, m.Conversation, m.TS))
	if err != nil {
		return false, 0, err
	}
	kind := ""
	switch {
	case !ok:
		kind = SlackNew
		if m.Deleted {
			kind = SlackDeleted
			m.Text, m.Files, m.Links, m.Mentions = "", nil, nil, nil
		}
		if _, err := tx.ExecContext(ctx, slackInsertMessageSQL, messageArgs(m)...); err != nil {
			return false, 0, err
		}
	case cur.Deleted:
		return false, 0, nil
	case m.Deleted:
		kind = SlackDeleted
		if _, err := tx.ExecContext(ctx, `UPDATE slack_messages SET deleted = 1, text = '', files = '[]', links = '[]', mentions = '[]'
			WHERE conversation = ? AND ts = ?`, m.Conversation, m.TS); err != nil {
			return false, 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM slack_reactions WHERE conversation = ? AND ts = ?`, m.Conversation, m.TS); err != nil {
			return false, 0, err
		}
	case compareSlackTS(m.EditedTS, cur.EditedTS) > 0:
		kind = SlackEdited
		if _, err := tx.ExecContext(ctx, `UPDATE slack_messages SET text = ?, edited_ts = ?, subtype = ?, files = ?, links = ?, mentions = ?,
			permalink = CASE WHEN ? = '' THEN permalink ELSE ? END WHERE conversation = ? AND ts = ?`,
			m.Text, m.EditedTS, m.Subtype, jsonText(m.Files), jsonText(m.Links), jsonText(m.Mentions), m.Permalink, m.Permalink,
			m.Conversation, m.TS); err != nil {
			return false, 0, err
		}
	case cur.Permalink == "" && m.Permalink != "":
		if _, err := tx.ExecContext(ctx, `UPDATE slack_messages SET permalink = ? WHERE conversation = ? AND ts = ?`,
			m.Permalink, m.Conversation, m.TS); err != nil {
			return false, 0, err
		}
	}
	if kind != "" {
		changed = true
		n, err := insertChange(ctx, tx, m, kind, m.EditedTS, now)
		if err != nil {
			return false, 0, err
		}
		rows += n
	}
	if m.Reactions == nil || m.Deleted {
		return changed, rows, nil
	}
	moved, err := replaceReactions(ctx, tx, m)
	if err != nil {
		return false, 0, err
	}
	if moved && ok {
		changed = true
		var ordinal int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM slack_changes WHERE conversation = ? AND ts = ? AND kind = ?`,
			m.Conversation, m.TS, SlackReaction).Scan(&ordinal); err != nil {
			return false, 0, err
		}
		n, err := insertChange(ctx, tx, m, SlackReaction, fmt.Sprintf("r:%d", ordinal+1), now)
		if err != nil {
			return false, 0, err
		}
		rows += n
	}
	return changed, rows, nil
}

func insertChange(ctx context.Context, tx *Tx, m MessageRow, kind, editedTS string, now time.Time) (int, error) {
	res, err := tx.ExecContext(ctx, slackInsertChangeSQL, m.Conversation, m.TS, m.ThreadTS, kind, editedTS, slackTime(now))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

type reactionKey struct{ name, user string }

// replaceReactions makes m's reactions the stored set; moved says whether
// the set changed.
func replaceReactions(ctx context.Context, tx *Tx, m MessageRow) (moved bool, err error) {
	var want []reactionKey
	for _, r := range m.Reactions {
		for _, u := range r.Users {
			want = append(want, reactionKey{r.Name, u})
		}
	}
	slices.SortFunc(want, func(a, b reactionKey) int { return cmp.Or(cmp.Compare(a.name, b.name), cmp.Compare(a.user, b.user)) })
	want = slices.Compact(want)
	got, err := storedReactions(ctx, tx, m.Conversation, m.TS)
	if err != nil {
		return false, err
	}
	if slices.Equal(got, want) {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM slack_reactions WHERE conversation = ? AND ts = ?`, m.Conversation, m.TS); err != nil {
		return false, err
	}
	for _, k := range want {
		if _, err := tx.ExecContext(ctx, `INSERT INTO slack_reactions (conversation, ts, name, user) VALUES (?, ?, ?, ?)`,
			m.Conversation, m.TS, k.name, k.user); err != nil {
			return false, err
		}
	}
	return true, nil
}

func storedReactions(ctx context.Context, r Reader, conv, ts string) ([]reactionKey, error) {
	rows, err := r.QueryContext(ctx, slackReactionsSQL, conv, ts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []reactionKey
	for rows.Next() {
		var k reactionKey
		if err := rows.Scan(&k.name, &k.user); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func messageArgs(m MessageRow) []any {
	return []any{m.Conversation, m.TS, m.ThreadTS, m.ParentUser, m.User, m.BotID, m.AppID, m.Username, m.Subtype, m.Text,
		m.EditedTS, boolInt(m.Deleted), jsonText(m.Files), jsonText(m.Links), jsonText(m.Mentions), m.Permalink, m.Via,
		slackTime(m.ReceivedAt)}
}

type rowScanner interface{ Scan(dest ...any) error }

func scanMessage(r rowScanner) (MessageRow, bool, error) {
	var m MessageRow
	var deleted int
	var files, links, mentions, received string
	err := r.Scan(&m.Conversation, &m.TS, &m.ThreadTS, &m.ParentUser, &m.User, &m.BotID, &m.AppID, &m.Username, &m.Subtype,
		&m.Text, &m.EditedTS, &deleted, &files, &links, &mentions, &m.Permalink, &m.Via, &received)
	if errors.Is(err, sql.ErrNoRows) {
		return MessageRow{}, false, nil
	}
	if err != nil {
		return MessageRow{}, false, err
	}
	m.Deleted = deleted != 0
	for _, f := range []struct {
		raw string
		v   any
	}{{files, &m.Files}, {links, &m.Links}, {mentions, &m.Mentions}} {
		if err := json.Unmarshal([]byte(f.raw), f.v); err != nil {
			return MessageRow{}, false, err
		}
	}
	if m.ReceivedAt, err = parseSlackTime(received); err != nil {
		return MessageRow{}, false, err
	}
	return m, true, nil
}

// MarkSeen moves conv's high-water mark to ts when it is later; it stores
// nothing of the message.
func (s *SlackStore) MarkSeen(ctx context.Context, conv, ts string) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error { return moveHWM(ctx, tx, conv, ts) })
	return err
}

// See stamps every object a pass returned as seen at now, and moves each to
// its next check in next.
func (s *SlackStore) See(ctx context.Context, objects []string, next map[string]time.Time, now time.Time) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		for _, o := range objects {
			if _, err := tx.ExecContext(ctx, `UPDATE slack_objects SET seen_at = ?,
				next_check_at = CASE WHEN ? = '' THEN next_check_at ELSE ? END WHERE object = ?`,
				slackTime(now), slackTime(next[o]), slackTime(next[o]), o); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// Watched are the watched objects whose next check is at or before now.
func (s *SlackStore) Watched(ctx context.Context, now time.Time) ([]ObjectRow, error) {
	rows, err := s.db.R().QueryContext(ctx, slackWatchedSQL, slackTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ObjectRow
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Object is an admitted or watched object.
func (s *SlackStore) Object(ctx context.Context, object string) (ObjectRow, bool, error) {
	o, err := scanObject(s.db.R().QueryRowContext(ctx, slackObjectSQL, object))
	if errors.Is(err, sql.ErrNoRows) {
		return ObjectRow{}, false, nil
	}
	return o, err == nil, err
}

// Versions are the stored versions of the objects the store knows.
func (s *SlackStore) Versions(ctx context.Context, objects []string) (map[string]string, error) {
	out := make(map[string]string, len(objects))
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		for _, o := range objects {
			var v string
			err := tx.QueryRowContext(ctx, `SELECT version FROM slack_objects WHERE object = ?`, o).Scan(&v)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			out[o] = v
		}
		return nil
	})
	return out, err
}

func scanObject(r rowScanner) (ObjectRow, error) {
	var o ObjectRow
	var watched int
	var reasons, last, next, seen, admitted string
	if err := r.Scan(&o.Object, &o.Conversation, &o.TS, &o.Kind, &o.Version, &o.LatestReply, &o.ReplyCount, &watched, &reasons,
		&last, &next, &seen, &admitted); err != nil {
		return ObjectRow{}, err
	}
	o.Watched = watched != 0
	if err := json.Unmarshal([]byte(reasons), &o.Reasons); err != nil {
		return ObjectRow{}, err
	}
	var err error
	for _, t := range []struct {
		raw string
		at  *time.Time
	}{{last, &o.LastActivityAt}, {next, &o.NextCheckAt}, {seen, &o.SeenAt}, {admitted, &o.AdmittedAt}} {
		if *t.at, err = parseSlackTime(t.raw); err != nil {
			return ObjectRow{}, err
		}
	}
	return o, nil
}

// Schedule sets the object's next check.
func (s *SlackStore) Schedule(ctx context.Context, object string, at time.Time) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE slack_objects SET next_check_at = ? WHERE object = ?`, slackTime(at), object)
		return err
	})
	return err
}

// SetWatched flips watched and adds reason to the object's reasons; an
// object the store lacks is created due now with no version, so the next
// pass reads it. Its messages stay either way.
func (s *SlackStore) SetWatched(ctx context.Context, object string, watched bool, reason string) error {
	conv, ts, _ := strings.Cut(object, "/")
	if conv == "" {
		return fmt.Errorf("slack object %q: no conversation", object)
	}
	kind := SlackThread
	if ts == "" {
		kind = SlackDM
	}
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT reasons FROM slack_objects WHERE object = ?`, object).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			if !watched {
				return nil
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO slack_objects (object, conversation, ts, kind, watched, reasons) VALUES (?, ?, ?, ?, 1, ?)`,
				object, conv, ts, kind, jsonText(nonEmpty(reason)))
			return err
		}
		if err != nil {
			return err
		}
		var reasons []string
		if err := json.Unmarshal([]byte(raw), &reasons); err != nil {
			return err
		}
		if !watched {
			reasons = []string{}
		} else if reason != "" && !slices.Contains(reasons, reason) {
			reasons = append(reasons, reason)
		}
		_, err = tx.ExecContext(ctx, `UPDATE slack_objects SET watched = ?, reasons = ? WHERE object = ?`, boolInt(watched), jsonText(reasons), object)
		return err
	})
	return err
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func (s *SlackStore) Conversations(ctx context.Context, ids []string) ([]ConversationRow, error) {
	list, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	return scanConversations(s.db.R().QueryContext(ctx, `SELECT `+slackConversationColumns+` FROM slack_conversations
		WHERE id IN (SELECT value FROM json_each(?)) ORDER BY id`, string(list)))
}

// AdmittedConversations are the conversations with an admitted object, with
// their hwm, for the catch-up.
func (s *SlackStore) AdmittedConversations(ctx context.Context) ([]ConversationRow, error) {
	return scanConversations(s.db.R().QueryContext(ctx, slackAdmittedSQL))
}

func scanConversations(rows *sql.Rows, err error) ([]ConversationRow, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConversationRow
	for rows.Next() {
		var c ConversationRow
		var member, archived, admitted int
		var updated string
		if err := rows.Scan(&c.ID, &c.Team, &c.Name, &c.Kind, &member, &archived, &c.DMUser, &c.HWM, &admitted, &updated); err != nil {
			return nil, err
		}
		c.IsMember, c.Archived, c.Admitted = member != 0, archived != 0, admitted != 0
		if c.UpdatedAt, err = parseSlackTime(updated); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PutConversations stores the conversations' metadata; hwm and admitted stay.
func (s *SlackStore) PutConversations(ctx context.Context, cs []ConversationRow) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error { return putConversations(ctx, tx, cs, time.Time{}) })
	return err
}

func putConversations(ctx context.Context, tx *Tx, cs []ConversationRow, now time.Time) error {
	for _, c := range cs {
		at := c.UpdatedAt
		if at.IsZero() {
			at = now
		}
		if err := touchRenamed(ctx, tx, c); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, slackPutConversationSQL, c.ID, c.Team, c.Name, c.Kind, boolInt(c.IsMember), boolInt(c.Archived),
			c.DMUser, slackTime(at)); err != nil {
			return fmt.Errorf("slack conversation %s: %w", c.ID, err)
		}
	}
	return nil
}

const slackProvider unit.ProviderID = "slack"

// touchRenamed notes the threads of c's conversation when c names it anew,
// so their rows show the name.
func touchRenamed(ctx context.Context, tx *Tx, c ConversationRow) error {
	if c.Name == "" {
		return nil
	}
	var name, team string
	err := tx.QueryRowContext(ctx, `SELECT name, team FROM slack_conversations WHERE id = ?`, c.ID).Scan(&name, &team)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if name == c.Name {
		return nil
	}
	if team = cmp.Or(c.Team, team); team == "" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT ts FROM slack_objects WHERE conversation = ? AND kind = ? AND ts != ''`, c.ID, SlackThread)
	if err != nil {
		return err
	}
	defer rows.Close()
	var units []unit.Unit
	for rows.Next() {
		var ts string
		if err := rows.Scan(&ts); err != nil {
			return err
		}
		units = append(units, unit.Unit{Provider: slackProvider, Key: team + "/" + c.ID + "/" + ts})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(units) > 0 {
		tx.Touch(units...)
	}
	return nil
}

// Permalink is a stored message permalink, "" when none is stored: what
// names the workspace's web host.
func (s *SlackStore) Permalink(ctx context.Context) (string, error) {
	var out string
	err := s.db.R().QueryRowContext(ctx, `SELECT permalink FROM slack_messages WHERE permalink LIKE 'https://%' LIMIT 1`).Scan(&out)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return out, err
}

func (s *SlackStore) Users(ctx context.Context, ids []string) ([]UserRow, error) {
	list, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	return scanUsers(s.db.R().QueryContext(ctx, `SELECT `+slackUserColumns+` FROM slack_users
		WHERE id IN (SELECT value FROM json_each(?)) ORDER BY id`, string(list)))
}

// UserByEmail matches the email without case; a live user wins over a deleted one.
func (s *SlackStore) UserByEmail(ctx context.Context, email string) (UserRow, bool, error) {
	if email == "" {
		return UserRow{}, false, nil
	}
	us, err := scanUsers(s.db.R().QueryContext(ctx, slackUserByEmail, email))
	if err != nil || len(us) == 0 {
		return UserRow{}, false, err
	}
	return us[0], true, nil
}

func scanUsers(rows *sql.Rows, err error) ([]UserRow, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserRow
	for rows.Next() {
		var u UserRow
		var bot, deleted int
		var fetched string
		if err := rows.Scan(&u.ID, &u.Name, &u.DisplayName, &u.Email, &bot, &deleted, &fetched); err != nil {
			return nil, err
		}
		u.IsBot, u.Deleted = bot != 0, deleted != 0
		if u.FetchedAt, err = parseSlackTime(fetched); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *SlackStore) PutUsers(ctx context.Context, us []UserRow) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error { return putUsers(ctx, tx, us) })
	return err
}

func putUsers(ctx context.Context, tx *Tx, us []UserRow) error {
	for _, u := range us {
		if _, err := tx.ExecContext(ctx, slackPutUserSQL, u.ID, u.Name, u.DisplayName, u.Email, boolInt(u.IsBot), boolInt(u.Deleted),
			slackTime(u.FetchedAt)); err != nil {
			return fmt.Errorf("slack user %s: %w", u.ID, err)
		}
	}
	return nil
}

// slackMessagesQuery is the statement and arguments of q over conv.
func slackMessagesQuery(conv string, q MessageQuery) (string, []any) {
	query, args := slackMessagesOfQuery, []any{conv}
	if q.Thread != "" || q.From != "" || q.To != "" || q.Before != "" {
		query = slackMessagesByAnchor
	}
	switch {
	case q.Thread != "":
		query, args = query+` AND `+slackAnchor+` = ?`, append(args, q.Thread)
	case q.From != "":
		query, args = query+` AND `+slackAnchor+` >= ?`, append(args, q.From)
	}
	if q.Thread == "" && q.To != "" {
		query, args = query+` AND `+slackAnchor+` <= ?`, append(args, q.To)
	}
	if q.Thread == "" && q.Before != "" {
		query, args = query+` AND `+slackAnchor+` < ?`, append(args, q.Before)
	}
	if q.TS != "" {
		query, args = query+` AND ts = ?`, append(args, q.TS)
	}
	switch {
	case q.After != "" && q.Thread != "":
		query, args = query+` AND +ts > ?`, append(args, q.After)
	case q.After != "":
		query, args = query+` AND ts > ?`, append(args, q.After)
	}
	if q.Newest {
		query += ` ORDER BY ` + slackAnchor + ` DESC, ts DESC`
	} else {
		query += ` ORDER BY ts`
	}
	if q.Limit > 0 {
		query, args = query+` LIMIT ?`, append(args, q.Limit)
	}
	return query, args
}

// Messages are conv's messages q selects, ascending ts, with their reactions.
// Slack timestamps compare as text: every ts has ten digits before the dot.
func (s *SlackStore) Messages(ctx context.Context, conv string, q MessageQuery) ([]MessageRow, error) {
	query, args := slackMessagesQuery(conv, q)
	var out []MessageRow
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			m, _, err := scanMessage(rows)
			if err != nil {
				rows.Close()
				return err
			}
			out = append(out, m)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		return loadReactions(ctx, tx, conv, out)
	})
	if q.Newest {
		slices.SortFunc(out, func(a, b MessageRow) int { return compareSlackTS(a.TS, b.TS) })
	}
	return out, err
}

func loadReactions(ctx context.Context, tx *sql.Tx, conv string, ms []MessageRow) error {
	if len(ms) == 0 {
		return nil
	}
	at := make(map[string]int, len(ms))
	ts := make([]string, len(ms))
	for i, m := range ms {
		at[m.TS], ts[i] = i, m.TS
	}
	rows, err := tx.QueryContext(ctx, slackReactionsOfSQL, conv, jsonText(ts))
	if err != nil {
		return err
	}
	defer rows.Close()
	var cur string
	var ks []reactionKey
	flush := func() {
		if len(ks) > 0 {
			ms[at[cur]].Reactions = groupReactions(ks)
		}
		ks = ks[:0]
	}
	for rows.Next() {
		var t string
		var k reactionKey
		if err := rows.Scan(&t, &k.name, &k.user); err != nil {
			return err
		}
		if t != cur {
			flush()
			cur = t
		}
		ks = append(ks, k)
	}
	flush()
	return rows.Err()
}

func groupReactions(ks []reactionKey) []ReactionRow {
	var out []ReactionRow
	for _, k := range ks {
		if n := len(out); n > 0 && out[n-1].Name == k.name {
			out[n-1].Users = append(out[n-1].Users, k.user)
			continue
		}
		out = append(out, ReactionRow{Name: k.name, Users: []string{k.user}})
	}
	return out
}

// Changes are the change rows after afterSeq, ascending, at most limit (0: all).
func (s *SlackStore) Changes(ctx context.Context, afterSeq int64, limit int) ([]ChangeRow, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.db.R().QueryContext(ctx, slackChangesSQL, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChangeRow
	for rows.Next() {
		var c ChangeRow
		var at string
		if err := rows.Scan(&c.Seq, &c.Conversation, &c.TS, &c.ThreadTS, &c.Kind, &c.EditedTS, &at); err != nil {
			return nil, err
		}
		if c.At, err = parseSlackTime(at); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Cursor is the named cursor's value, "" when unset.
func (s *SlackStore) Cursor(ctx context.Context, name string) (string, error) {
	var v string
	err := s.db.R().QueryRowContext(ctx, slackCursorSQL, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *SlackStore) SetCursor(ctx context.Context, name, value string) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, slackSetCursorSQL, name, value, slackTime(time.Now()))
		return err
	})
	return err
}
