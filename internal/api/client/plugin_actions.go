package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
)

// PluginAction runs plugin id's action with args and decodes the plugin's
// answer into out; a nil out discards it.
func (c *Client) PluginAction(ctx context.Context, id, action string, args, out any) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	var res json.RawMessage
	if err := c.do(ctx, actionTimeout, http.MethodPost, api.PluginActionPath(id, action), api.PluginActionRequest{Args: raw}, &res); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(res, out)
}

// PluginRun runs the command line args of running plugin id, copying what
// it writes to stdout and stderr as it writes it, and answers its exit
// code.
func (c *Client) PluginRun(ctx context.Context, id string, args []string, stdout, stderr io.Writer) (int, error) {
	b, err := json.Marshal(api.PluginRunRequest{Args: args})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+brand.Name+api.PluginRunPath(id), bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.send(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return 0, refused(resp)
	}
	dec := json.NewDecoder(resp.Body)
	for {
		var ch api.PluginRunChunk
		if err := dec.Decode(&ch); err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return 0, fmt.Errorf("plugin %s's command ended without an exit: %w", id, err)
		}
		switch {
		case ch.Error != "":
			return 0, errors.New(ch.Error)
		case ch.Exit != nil:
			return *ch.Exit, nil
		}
		if _, err := io.WriteString(stdout, ch.Stdout); err != nil {
			return 0, err
		}
		if _, err := io.WriteString(stderr, ch.Stderr); err != nil {
			return 0, err
		}
	}
}
