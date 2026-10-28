package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// forgeCommand runs a command of the forge's plugin; an exit other than 0
// is an error carrying what the command wrote to its stderr.
func (e *Env) forgeCommand(ctx context.Context, args ...string) error {
	var stderr bytes.Buffer
	exit, err := e.forgeCalls().RunCLI(ctx, src.CLIRequest{Args: args}, io.Discard, &stderr)
	if err != nil {
		return err
	}
	if exit != 0 {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(msg)
		}
		return fmt.Errorf("%s exited %d", strings.Join(args, " "), exit)
	}
	return nil
}
