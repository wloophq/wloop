package orca

import "context"

// RPC calls an Orca runtime method directly. Some parameters the app uses,
// such as the branch name of a worktree created from a pull request, have no
// `orca` CLI flag.
type RPC interface {
	Call(ctx context.Context, method string, params any) ([]byte, error)
}
