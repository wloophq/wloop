package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/wloophq/wloop/internal/api"
)

const gitAccessKey = "git_access/"

// GitAccessLog keeps, per host, the latest git network operation and the
// latest success, in the store's meta table.
type GitAccessLog struct {
	Store Metas
	mu    sync.Mutex
}

func (l *GitAccessLog) Record(ctx context.Context, r api.GitAccessRecord) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := gitAccessKey + r.Host
	g := api.GitAccess{Host: r.Host}
	if raw, err := l.Store.Meta(ctx, key); err != nil {
		return err
	} else if raw != "" {
		_ = json.Unmarshal([]byte(raw), &g)
	}
	if r.At.Before(g.Last.At) {
		return nil
	}
	g.Last = r.GitAccessOutcome
	if r.Error == "" {
		ok := r.GitAccessOutcome
		g.LastOK = &ok
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	return l.Store.SetMeta(ctx, key, string(raw))
}

// Hosts are the hosts seen so far, sorted by name.
func (l *GitAccessLog) Hosts(ctx context.Context) ([]api.GitAccess, error) {
	all, err := l.Store.MetaPrefixed(ctx, gitAccessKey)
	if err != nil {
		return nil, err
	}
	var out []api.GitAccess
	for _, raw := range all {
		var g api.GitAccess
		if json.Unmarshal([]byte(raw), &g) == nil && g.Host != "" {
			out = append(out, g)
		}
	}
	slices.SortFunc(out, func(a, b api.GitAccess) int { return strings.Compare(a.Host, b.Host) })
	return out, nil
}

func (s *Server) handleGitAccess(w http.ResponseWriter, r *http.Request) {
	if s.deps.GitAccess == nil {
		writeError(w, http.StatusServiceUnavailable, "this daemon keeps no git access log")
		return
	}
	var rec api.GitAccessRecord
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil || rec.Host == "" || rec.Op == "" || rec.At.IsZero() {
		writeError(w, http.StatusBadRequest, "a git access record takes host, op and at")
		return
	}
	if err := s.deps.GitAccess.Record(r.Context(), rec); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
