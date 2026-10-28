// Package gh shells out to the gh command line, which owns GitHub's
// authentication.
package gh

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Runner interface {
	// Run returns stdout even when gh exits non-zero, so a GraphQL response
	// with partial errors still yields its data.
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// Exec runs Bin, gh on the PATH when empty.
type Exec struct{ Bin string }

// Timeout bounds one gh call.
const Timeout = 2 * time.Minute

// Run runs gh with args. A gh that is not installed is an error wrapping
// exec.ErrNotFound.
func (e Exec) Run(ctx context.Context, args ...string) ([]byte, error) {
	bin := e.Bin
	if bin == "" {
		bin = "gh"
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return stdout.Bytes(), fmt.Errorf("gh %s: %w", summarize(args), err)
		}
		return stdout.Bytes(), fmt.Errorf("gh %s: %s", summarize(args), msg)
	}
	return stdout.Bytes(), nil
}

func summarize(args []string) string {
	if len(args) > 3 {
		args = args[:3]
	}
	return strings.Join(args, " ")
}
