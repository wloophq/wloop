package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/usage"
)

// Per-call caps: every call carries one, and the run's budget reserves it
// before the call starts.
const (
	ExtractorCallUSD = 0.5
	JudgeCallUSD     = 1.0
)

// Key says what produced a stage file; a cached file is reused only under
// the same key.
type Key struct {
	Tier      string `json:"tier"`
	Language  string `json:"language"`
	Now       string `json:"now"`
	PromptSHA string `json:"prompt_sha"`
}

type sidecar struct {
	Key
	CostUSD float64 `json:"cost_usd"`
	Model   string  `json:"model,omitempty"`
	// Tokens is nil when the agent did not report them.
	Tokens *Tokens `json:"tokens,omitempty"`
}

// Tokens are what one call read and wrote.
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

func tokensOf(t *usage.Tokens) *Tokens {
	if t == nil {
		return nil
	}
	return &Tokens{Input: t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite}
}

func keyPath(path string) string { return strings.TrimSuffix(path, ".json") + ".key.json" }

func promptSHA(prompt, schema string) string {
	h := sha256.Sum256([]byte(prompt + "\x00" + schema))
	return hex.EncodeToString(h[:])
}

// StaleError is a stage file whose key differs from the call's.
type StaleError struct{ Path string }

func (e *StaleError) Error() string {
	return fmt.Sprintf("%s was produced under another key (tier, language, now or prompt); move it away or use another --out or --label", e.Path)
}

var ErrBudget = errors.New("the run stopped before passing --max-usd")

// budget reserves each call's cap before it starts, so the calls in flight
// can never take the run past max. A call that fits only once others settle
// waits for them; one that cannot fit stops the run.
type budget struct {
	mu                   sync.Mutex
	settled              *sync.Cond
	max, spent, reserved float64
	stopped              bool
}

func newBudget(max float64) *budget {
	b := &budget{max: max}
	b.settled = sync.NewCond(&b.mu)
	return b
}

func (b *budget) reserve(c float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		if b.stopped || b.spent+c > b.max+1e-9 {
			b.stopped = true
			return false
		}
		if b.spent+b.reserved+c <= b.max+1e-9 {
			b.reserved += c
			return true
		}
		b.settled.Wait()
	}
}

func (b *budget) settle(reserved, cost float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reserved -= reserved
	b.spent += cost
	b.settled.Broadcast()
}

func (b *budget) state() (spent float64, stopped bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spent, b.stopped
}

// stage is one cached call: its file, its key and what it may spend.
type stage struct {
	inner    turns.Asker
	budget   *budget
	path     string
	language string
	now      string
	override func(string) string
}

func (s stage) key(q turns.Ask) Key {
	return Key{Tier: string(q.Tier), Language: s.language, Now: s.now, PromptSHA: promptSHA(q.Prompt, q.Schema)}
}

// cached is the stage file when its key matches k, nil when there is none.
func (s stage) cached(k Key) ([]byte, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sc sidecar
	if err := readJSON(keyPath(s.path), &sc); err != nil || sc.Key != k {
		return nil, &StaleError{Path: s.path}
	}
	return raw, nil
}

// Ask answers from the stage file, or makes the call within the budget and
// writes the file with its key.
func (s stage) Ask(ctx context.Context, q turns.Ask) (turns.Answer, error) {
	if s.override != nil {
		q.Prompt = s.override(q.Prompt)
	}
	k := s.key(q)
	raw, err := s.cached(k)
	if err != nil {
		return turns.Answer{}, err
	}
	if raw != nil {
		return turns.Answer{JSON: raw}, nil
	}
	if !s.budget.reserve(q.MaxCostUSD) {
		return turns.Answer{}, ErrBudget
	}
	ans, err := s.inner.Ask(ctx, q)
	cost := q.MaxCostUSD
	if ans.CostUSD != nil {
		cost = *ans.CostUSD
	}
	s.budget.settle(q.MaxCostUSD, cost)
	if err != nil {
		return ans, err
	}
	var doc any
	if json.Unmarshal(ans.JSON, &doc) != nil {
		return ans, fmt.Errorf("%w: the answer is not JSON", turns.ErrUnfit)
	}
	if err := writeJSON(s.path, doc); err != nil {
		return ans, err
	}
	return ans, writeJSON(keyPath(s.path), sidecar{Key: k, CostUSD: cost, Model: ans.Model, Tokens: tokensOf(ans.Tokens)})
}

// forget removes a stage file whose answer turned out unfit, so the next
// run asks again.
func (s stage) forget() {
	os.Remove(s.path)
	os.Remove(keyPath(s.path))
}

// stageSidecar is what the call behind a stage file spent; false when
// there is no such call.
func stageSidecar(path string) (sidecar, bool) {
	var sc sidecar
	if readJSON(keyPath(path), &sc) != nil {
		return sidecar{}, false
	}
	return sc, true
}

// fill replaces each placeholder of tmpl once, in one pass, so a value that
// holds a placeholder stays as written.
func fill(tmpl string, values map[string]string) string {
	var pairs []string
	for k, v := range values {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}
