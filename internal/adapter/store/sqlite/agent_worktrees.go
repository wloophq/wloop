package sqlite

import (
	"context"
	"os"
)

// AgentWorktree is a worktree an agent session of the loop ran in, with the agent's home.
type AgentWorktree struct{ Home, Worktree string }

// AgentWorktrees lists the distinct worktrees the loop's sessions ran in.
func (db *DB) AgentWorktrees(ctx context.Context) ([]AgentWorktree, error) {
	rows, err := db.R().QueryContext(ctx, `SELECT home, worktree FROM agent_sessions WHERE worktree != ''
		UNION SELECT home, worktree FROM sessions WHERE worktree != '' ORDER BY worktree, home`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentWorktree
	for rows.Next() {
		var w AgentWorktree
		if err := rows.Scan(&w.Home, &w.Worktree); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// OpenReadOnly opens the store at path for reads only, for a process that is not the store's
// writer; it creates nothing and refuses a store at another version.
func OpenReadOnly(path string) (*DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	if err := Check(path); err != nil {
		return nil, err
	}
	r, err := handle(path+readerDSN, 1)
	if err != nil {
		return nil, err
	}
	return &DB{w: r, r: r, writer: &writeLock{}}, nil
}
