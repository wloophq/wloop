package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
)

// runConfig prints a key of the effective configuration, so skills read
// the same values the daemon uses.
func runConfig(e *Env, args []string) error {
	if len(args) != 2 || args[0] != "get" {
		return errors.New("usage: " + brand.Command + " config get <key>")
	}
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return err
	}
	for part := range strings.SplitSeq(args[1], ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("no config key %q", args[1])
		}
		if v, ok = m[part]; !ok {
			return fmt.Errorf("no config key %q", args[1])
		}
	}
	if s, ok := v.(string); ok {
		_, err := fmt.Fprintln(e.Out, s)
		return err
	}
	out, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(e.Out, string(out))
	return err
}
