package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/wloophq/wloop/internal/brief"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/pr"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// briefInputs are the brief queue's reads of a pull request through the
// GitHub plugin's BriefInput, which reads its facts and its diff in one
// call: the queue's Facts and Diff of one ref share that call.
type briefInputs struct {
	plugin func() (briefReader, error)
	// calls are the plugin calls in flight, which the Env's stop waits on.
	calls *sync.WaitGroup

	mu       sync.Mutex
	inflight map[pr.Ref]*briefRead
}

type briefReader interface {
	BriefInput(ctx context.Context, u plugin.Unit) (src.BriefInput, error)
}

// briefRead is one BriefInput call of a ref, which Facts and Diff each take
// once.
type briefRead struct {
	done          chan struct{}
	in            brief.Input
	diff          string
	cut           bool
	err           error
	diffErr       error
	facts, diffed bool
}

// briefParts are the GitHub plugin's parts of a brief input.
type briefParts struct {
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Issue  string   `json:"issue"`
	Files  []string `json:"files"`
	Listed []string `json:"listed"`
	// DiffError is why GitHub served no diff.
	DiffError string `json:"diff_error"`
}

func newBriefInputs(calls *sync.WaitGroup, plugin func() (briefReader, error)) *briefInputs {
	return &briefInputs{plugin: plugin, calls: calls, inflight: map[pr.Ref]*briefRead{}}
}

// read is ref's read the other of Facts and Diff left, or a new one.
func (b *briefInputs) read(ctx context.Context, ref pr.Ref, facts bool) *briefRead {
	b.mu.Lock()
	r, ok := b.inflight[ref]
	if ok && (facts && r.facts || !facts && r.diffed) {
		ok = false
	}
	if !ok {
		r = &briefRead{done: make(chan struct{})}
		b.inflight[ref] = r
		b.calls.Go(func() { b.call(context.WithoutCancel(ctx), ref, r) })
	}
	if facts {
		r.facts = true
	} else {
		r.diffed = true
	}
	if r.facts && r.diffed {
		delete(b.inflight, ref)
	}
	b.mu.Unlock()
	select {
	case <-r.done:
	case <-ctx.Done():
		return &briefRead{err: ctx.Err()}
	}
	return r
}

func (b *briefInputs) call(ctx context.Context, ref pr.Ref, r *briefRead) {
	defer close(r.done)
	c, err := b.plugin()
	if err != nil {
		r.err = err
		return
	}
	priority := src.PriorityUser
	if dgh.PriorityOf(ctx) == dgh.PriorityBackground {
		priority = src.PriorityBackground
	}
	got, err := c.BriefInput(src.WithCall(ctx, src.Call{Priority: priority}), psource.Wire(ref.Unit()))
	if errors.Is(err, src.ErrUnavailable) {
		err = fmt.Errorf("%w: %w", dgh.ErrBudget, err)
	}
	if err != nil {
		r.err = err
		return
	}
	var parts briefParts
	if err := json.Unmarshal(got.Parts, &parts); err != nil {
		r.err = fmt.Errorf("%s: the plugin's brief input: %w", ref, err)
		return
	}
	r.in = brief.Input{Title: parts.Title, Body: parts.Body, Issue: parts.Issue, Files: parts.Listed, Paths: parts.Files}
	r.diff, r.cut = got.Text, got.Cut
	if parts.DiffError != "" {
		r.diffErr = errors.New(parts.DiffError)
	}
}

// Facts are ref's title, body, closing issue and files.
func (b *briefInputs) Facts(ctx context.Context, ref pr.Ref) (brief.Input, error) {
	r := b.read(ctx, ref, true)
	return r.in, r.err
}

// Diff is ref's diff as the plugin cut it at the configured bytes.
func (b *briefInputs) Diff(ctx context.Context, ref pr.Ref, _ int) (string, bool, error) {
	r := b.read(ctx, ref, false)
	if r.err != nil {
		return "", false, r.err
	}
	return r.diff, r.cut, r.diffErr
}
