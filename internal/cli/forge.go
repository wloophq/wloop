package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// forgeCalls are what the round commands ask the forge's plugin: the viewer,
// and the commands it serves without the daemon, such as a clone.
type forgeCalls interface {
	Identity(ctx context.Context) (src.Identity, error)
	RunCLI(ctx context.Context, req src.CLIRequest, stdout, stderr io.Writer) (int, error)
}

// forgeViewer is the viewer's login as the forge's plugin knows them.
func (e *Env) forgeViewer(ctx context.Context) (string, error) {
	id, err := e.forgeCalls().Identity(ctx)
	if err != nil {
		return "", err
	}
	if id.Viewer.Name == "" {
		return "", errors.New("the forge's plugin knows no viewer")
	}
	return id.Viewer.Name, nil
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
