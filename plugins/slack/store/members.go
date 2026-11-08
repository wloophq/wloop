package store

import (
	"context"
	"encoding/json"
	"time"
)

// MembersRow is a conversation's member user ids as conversations.members read them at Fetched.
type MembersRow struct {
	Conversation string
	Members      []string
	Fetched      time.Time
}

// Members are the stored members of the conversations ids, by conversation; a conversation never read is
// absent.
func (s *SlackStore) Members(ctx context.Context, ids []string) (map[string]MembersRow, error) {
	list, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.R().QueryContext(ctx, `SELECT conversation, members, fetched_at FROM slack_members
		WHERE conversation IN (SELECT value FROM json_each(?))`, string(list))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]MembersRow{}
	for rows.Next() {
		var r MembersRow
		var members, at string
		if err := rows.Scan(&r.Conversation, &members, &at); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(members), &r.Members); err != nil {
			return nil, err
		}
		r.Fetched = parseTime(at)
		out[r.Conversation] = r
	}
	return out, rows.Err()
}

// PutMembers stores r, replacing what its conversation held.
func (s *SlackStore) PutMembers(ctx context.Context, r MembersRow) error {
	raw, err := json.Marshal(r.Members)
	if err != nil {
		return err
	}
	_, err = s.db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO slack_members (conversation, members, fetched_at) VALUES (?, ?, ?)
			ON CONFLICT (conversation) DO UPDATE SET members = excluded.members, fetched_at = excluded.fetched_at`,
			r.Conversation, string(raw), formatTime(r.Fetched))
		return err
	})
	return err
}
