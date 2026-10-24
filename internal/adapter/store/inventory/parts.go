package inventory

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var headingBreak = regexp.MustCompile(`\n#{1,3}[ \t]`)

// splitParts cuts text at each line break followed by a markdown heading of
// level 1 to 3; the break itself belongs to no part, so joinParts restores
// the text exactly.
func splitParts(text string) []string {
	var parts []string
	start := 0
	for _, loc := range headingBreak.FindAllStringIndex(text, -1) {
		parts = append(parts, text[start:loc[0]])
		start = loc[0] + 1
	}
	return append(parts, text[start:])
}

func joinParts(parts []string) string { return strings.Join(parts, "\n") }

// partSHA is the key of a part in instruction_parts.
func partSHA(part string) string {
	sum := sha256.Sum256([]byte(part))
	return hex.EncodeToString(sum[:])
}

// deflatePart compresses a part for instruction_parts.body.
func deflatePart(part string) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, 6)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte(part)); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func inflatePart(body []byte) (string, error) {
	out, err := io.ReadAll(flate.NewReader(bytes.NewReader(body)))
	return string(out), err
}

// readParts returns the text the part list names.
func readParts(ctx context.Context, tx *sql.Tx, list string) (string, error) {
	var shas []string
	if err := json.Unmarshal([]byte(list), &shas); err != nil {
		return "", fmt.Errorf("part list %q: %w", list, err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.body FROM json_each(?) j LEFT JOIN instruction_parts p ON p.sha = j.value ORDER BY j.key`, list)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	parts := make([]string, 0, len(shas))
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return "", err
		}
		if body == nil {
			return "", fmt.Errorf("instruction part missing from %s", list)
		}
		part, err := inflatePart(body)
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(parts) != len(shas) {
		return "", fmt.Errorf("instruction parts: %d of %d read", len(parts), len(shas))
	}
	return joinParts(parts), nil
}
