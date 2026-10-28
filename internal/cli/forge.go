package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// forgeCalls are what the commands ask the forge's plugin: the viewer, its
// actions, such as reading a pull request, and the commands it serves
// without the daemon, such as a clone.
type forgeCalls interface {
	Identity(ctx context.Context) (src.Identity, error)
	Action(ctx context.Context, req src.ActionRequest) (json.RawMessage, error)
	RunCLI(ctx context.Context, req src.CLIRequest, stdout, stderr io.Writer) (int, error)
}

// forgeExit is a command of the forge's plugin that exited other than 0,
// with what it wrote to its stderr.
type forgeExit struct {
	args []string
	code int
	msg  string
}

func (x *forgeExit) Error() string {
	if x.msg != "" {
		return x.msg
	}
	return fmt.Sprintf("%s exited %d", strings.Join(x.args, " "), x.code)
}

// forgeCommand runs a command of the forge's plugin; an exit other than 0
// is a *forgeExit.
func (e *Env) forgeCommand(ctx context.Context, args ...string) error {
	_, err := e.forgeOutput(ctx, args...)
	return err
}

// forgeOutput is what a command of the forge's plugin writes to its stdout.
func (e *Env) forgeOutput(ctx context.Context, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	exit, err := e.forgeCalls().RunCLI(ctx, src.CLIRequest{Args: args}, &stdout, &stderr)
	if err != nil {
		return nil, err
	}
	if exit != 0 {
		return nil, &forgeExit{args: args, code: exit, msg: strings.TrimSpace(stderr.String())}
	}
	return stdout.Bytes(), nil
}
