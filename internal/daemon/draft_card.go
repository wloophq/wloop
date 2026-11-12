package daemon

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// DraftKinds are the draft kinds the plugins' manifests declare, as a draft's card reads them: a kind's spec
// and its payload's schema.
type DraftKinds interface {
	Lookup(provider, kind string) (draft.KindSpec, bool)
	Schema(spec draft.KindSpec) (json.RawMessage, error)
}

// cardFields fills v, d's view, with what its card shows of d beyond its text: its payload field by field, its
// basis, whether it is stale, its hold, and the profile's notes on how it stands; g is d's gate.
func (s *Server) cardFields(ctx context.Context, d draft.Draft, g gate.Gate, v *api.Draft) {
	publisher := string(s.publisherOf(d))
	if len(d.Payload) > 0 {
		v.Payload = slices.Clone(d.Payload)
		v.Fields = s.payloadFields(publisher, string(d.Kind), d.Payload)
	}
	v.Preview = s.preview(d, publisher, v.Where)
	v.Basis, v.Hold = d.Basis, d.Hold
	if spec, ok := s.kindSpec(d); ok {
		v.TextLocked = !slices.Contains(spec.Editable, editableText)
	}
	words := s.loaded().Gates.Drafts
	v.By = string(d.By.Kind)
	v.Author = words.Authors[v.By]
	if d.State == draft.Held && d.Basis != "" {
		var known bool
		v.Stale, known = s.stale(ctx, d)
		v.StaleUnknown = !known
	}
	if v.Stale && words.Stale != "" {
		v.Notes = append(v.Notes, words.Stale)
	}
	if d.Hold && d.State == draft.Held && words.Hold != "" {
		v.Notes = append(v.Notes, words.Hold)
	}
	if rule, ok := byRule(g); ok && d.State == draft.Sent {
		v.ByRule = rule
		v.Notes = append(v.Notes, s.byRuleText(d, rule, v.Where))
	}
}

// byRule is the rule of the user's that released g, false when a person, an agent, a window or nothing did.
func byRule(g gate.Gate) (string, bool) {
	ok := g.Status == gate.Approved && g.By == task.ActorWorkflow && g.Rule != "" && g.Rule != gates.RuleWindow
	return g.Rule, ok
}

func (s *Server) byRuleText(d draft.Draft, rule, where string) string {
	return s.notice(builtin.NoticeDraftByRule, map[string]any{"Draft": int64(d.ID), "Where": cmp.Or(where, d.Target, d.Unit.Label()), "Rule": rule,
		"Link": d.Evidence.Permalink})
}

// stale is whether held draft d's unit moved past its basis, and known whether that could be told: a refusal of d
// as stale at its rev tells it; else the unit's live basis, read once per unit and kind for every draft of it within
// basisFresh, and waited for only until the page's lookup deadline. A read that failed or has not answered yet
// leaves it unknown, which offers the approval: approving and sending re-check the basis live.
func (s *Server) stale(ctx context.Context, d draft.Draft) (stale, known bool) {
	if rev, ok := s.refusedStale.Load(d.ID); ok && rev.(int64) == d.Rev {
		return true, true
	}
	by := time.Now().Add(s.whereTimeout())
	if page, ok := ctx.Value(whereByKey{}).(time.Time); ok && page.Before(by) {
		by = page
	}
	ctx, cancel := context.WithDeadline(ctx, by)
	defer cancel()
	if check := s.deps.Stale; check != nil {
		stale, err := check(ctx, d)
		return stale, err == nil
	}
	o := s.deps.Outbox
	if o == nil || o.Bases == nil {
		return false, false
	}
	r := s.basisRead(o.Bases, d.Unit, string(d.Kind))
	select {
	case <-r.done:
	case <-ctx.Done():
		return false, false
	}
	if r.err != nil {
		return false, false
	}
	return r.live != d.Basis, true
}

// basisFresh is how long a unit's live basis read serves its drafts' cards; basisReadTimeout bounds one read.
const (
	basisFresh       = 30 * time.Second
	basisReadTimeout = 10 * time.Second
)

// basisRead is one live read of a unit's basis: done closes when live or err is set, at when it was started.
type basisRead struct {
	done chan struct{}
	live string
	err  error
	at   time.Time
}

// basisRead is the live read of u's basis for kind its cards share: the one in flight, else the last one while it
// is fresh, else a new one, which runs past the page that started it so the next page finds its answer.
func (s *Server) basisRead(bases outbox.Bases, u unit.Unit, kind string) *basisRead {
	key := u.String() + "\x00" + kind
	now := time.Now()
	s.basisMu.Lock()
	defer s.basisMu.Unlock()
	if r, ok := s.basisReads[key]; ok && now.Sub(r.at) < basisFresh {
		return r
	}
	if s.basisReads == nil {
		s.basisReads = map[string]*basisRead{}
	}
	r := &basisRead{done: make(chan struct{}), at: now}
	s.basisReads[key] = r
	s.ran.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), basisReadTimeout)
		defer cancel()
		r.live, r.err = bases.Live(ctx, u, kind)
		if r.err != nil && !errors.Is(r.err, context.DeadlineExceeded) && !errors.Is(r.err, context.Canceled) {
			s.logf("drafts: the live basis of %s: %v", u, r.err)
		}
		close(r.done)
	})
	return r
}

// RuleSent tells the user that a rule of theirs posted d, once it is sent; the composition root runs it after each send.
func (s *Server) RuleSent(ctx context.Context, d draft.Draft) error {
	g, ok, err := s.deps.Outbox.GateOf(ctx, d)
	if err != nil || !ok {
		return err
	}
	rule, ok := byRule(g)
	if !ok {
		return nil
	}
	s.notify(api.Notice{ID: fmt.Sprintf("draft:%d:by-rule", d.ID), Unit: d.Unit, Text: s.byRuleText(d, rule, s.draftWhere(ctx, d))})
	return nil
}

// preview is d's summary line: its kind's preview template, from the profile, over d's fields and its payload;
// "" when the kind has none or it does not render.
func (s *Server) preview(d draft.Draft, provider, where string) string {
	src := s.loaded().Previews[provider][string(d.Kind)]
	if src == "" {
		return ""
	}
	tmpl, ok := s.previews.Load(src)
	if !ok {
		parsed, err := template.New(string(d.Kind)).Option("missingkey=error").Parse(src)
		if err != nil {
			s.logf("drafts: the %s preview of %s does not parse: %v", d.Kind, provider, err)
			return ""
		}
		tmpl, _ = s.previews.LoadOrStore(src, parsed)
	}
	var payload any
	if len(d.Payload) > 0 {
		_ = json.Unmarshal(d.Payload, &payload)
	}
	data := map[string]any{"Kind": string(d.Kind), "Purpose": d.Purpose, "Delivery": string(d.Delivery), "Target": d.Target, "Where": where,
		"Text": d.Text, "Reaction": d.Reaction, "Basis": d.Basis, "Payload": payload}
	var out strings.Builder
	if err := tmpl.(*template.Template).Execute(&out, data); err != nil {
		s.logf("drafts: the %s preview of draft %d: %v", d.Kind, d.ID, err)
		return ""
	}
	return strings.TrimSpace(out.String())
}

// editableText is what a kind's editable names to let a person change a draft's text.
const editableText = "text"

// kindSpec is d's kind as its publisher's manifest declares it; false when none does or no kinds are served.
func (s *Server) kindSpec(d draft.Draft) (draft.KindSpec, bool) {
	if s.deps.DraftKinds == nil {
		return draft.KindSpec{}, false
	}
	return s.deps.DraftKinds.Lookup(string(s.publisherOf(d)), string(d.Kind))
}

// checkEditable refuses a person's change of d its kind does not let a person make: text other than d's on a
// kind whose editable lacks text, or a payload value changed outside its editable pointers.
func (s *Server) checkEditable(d draft.Draft, text *string, payload *json.RawMessage) error {
	spec, ok := s.kindSpec(d)
	if !ok {
		return nil
	}
	if text != nil && *text != d.Text && !slices.Contains(spec.Editable, editableText) {
		return badRequest{fmt.Sprintf("a %s draft's text is not yours to change", d.Kind)}
	}
	if payload == nil {
		return nil
	}
	var before, after any
	if len(d.Payload) > 0 {
		if err := json.Unmarshal(d.Payload, &before); err != nil {
			return badRequest{fmt.Sprintf("draft %d's payload does not decode: %v", d.ID, err)}
		}
	}
	if err := json.Unmarshal(*payload, &after); err != nil {
		return badRequest{fmt.Sprintf("payload is the draft's payload as JSON: %v", err)}
	}
	for _, p := range changedPointers("", before, after) {
		if !slices.ContainsFunc(spec.Editable, func(e string) bool { return e != editableText && (p == e || strings.HasPrefix(p, e+"/")) }) {
			return badRequest{fmt.Sprintf("%q of a %s draft is not yours to change", p, d.Kind)}
		}
	}
	return nil
}

// changedPointers are the JSON pointers under at where a and b differ, as deep as both are objects or lists of
// one length.
func changedPointers(at string, a, b any) []string {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			break
		}
		var out []string
		for k := range x {
			out = append(out, changedPointers(at+"/"+escapePointer(k), x[k], y[k])...)
		}
		for k := range y {
			if _, seen := x[k]; !seen {
				out = append(out, at+"/"+escapePointer(k))
			}
		}
		return out
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			break
		}
		var out []string
		for i := range x {
			out = append(out, changedPointers(at+"/"+strconv.Itoa(i), x[i], y[i])...)
		}
		return out
	}
	if reflect.DeepEqual(a, b) {
		return nil
	}
	return []string{at}
}

// payloadFields are the values of payload, a draft of kind's of provider, in its order, each under the plugin's
// word for its key, with the kind's editable fields' options from its schema.
func (s *Server) payloadFields(provider, kind string, payload json.RawMessage) []api.DraftField {
	root, err := decodeOrdered(json.NewDecoder(bytes.NewReader(payload)))
	if err != nil {
		s.logf("drafts: a %s payload does not decode: %v", kind, err)
		return []api.DraftField{{Pointer: "", Label: kind, Value: string(payload)}}
	}
	w := payloadWalk{words: s.loaded().UI[provider].Drafts[kind].Fields}
	if k := s.deps.DraftKinds; k != nil {
		if spec, ok := k.Lookup(provider, kind); ok {
			w.editable, w.schema = spec.Editable, s.kindSchema(k, spec)
		}
	}
	if root.kind == nodeScalar {
		return []api.DraftField{w.field(root, "", kind, w.schema)}
	}
	return w.children(root, "", "", w.schema)
}

// kindSchema is spec's payload schema, read once while it reads.
func (s *Server) kindSchema(k DraftKinds, spec draft.KindSpec) map[string]any {
	key := spec.Provider + "/" + spec.Name
	if m, ok := s.kindSchemas.Load(key); ok {
		return m.(map[string]any)
	}
	raw, err := k.Schema(spec)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	s.kindSchemas.Store(key, m)
	return m
}

type payloadWalk struct {
	words    map[string]intent.UIDraftField
	editable []string
	schema   map[string]any
}

func (w payloadWalk) children(n node, pointer, key string, schema map[string]any) []api.DraftField {
	out := make([]api.DraftField, 0, len(n.vals))
	for i, v := range n.vals {
		if n.kind == nodeArray {
			p := pointer + "/" + strconv.Itoa(i)
			f := w.field(v, p, key, schemaAt(schema, "items"))
			f.Label = strconv.Itoa(i + 1)
			out = append(out, f)
			continue
		}
		k := n.keys[i]
		out = append(out, w.field(v, pointer+"/"+escapePointer(k), k, schemaAt(schemaAt(schema, "properties"), k)))
	}
	return out
}

// field is n, the value of key at pointer, whose schema is schema.
func (w payloadWalk) field(n node, pointer, key string, schema map[string]any) api.DraftField {
	words := w.words[key]
	f := api.DraftField{Pointer: pointer, Label: cmp.Or(words.Label, key)}
	if n.kind != nodeScalar {
		f.Fields = w.children(n, pointer, key, schema)
		return f
	}
	f.Value, f.Shown = n.scalar, words.Values[n.scalar]
	if slices.Contains(w.editable, pointer) {
		enum, _ := schema["enum"].([]any)
		for _, e := range enum {
			if v, ok := e.(string); ok {
				f.Options = append(f.Options, api.DraftOption{Value: v, Label: cmp.Or(words.Values[v], v)})
			}
		}
		f.Editable = len(f.Options) > 0
	}
	return f
}

func schemaAt(schema map[string]any, key string) map[string]any {
	m, _ := schema[key].(map[string]any)
	return m
}

func escapePointer(k string) string {
	return strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
}

type nodeKind int

const (
	nodeScalar nodeKind = iota
	nodeObject
	nodeArray
)

// node is a JSON value that keeps its object's keys in the order they were written.
type node struct {
	kind   nodeKind
	scalar string
	keys   []string
	vals   []node
}

func decodeOrdered(dec *json.Decoder) (node, error) {
	dec.UseNumber()
	n, err := decodeNode(dec)
	if err != nil {
		return node{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return node{}, errors.New("more than one value")
	}
	return n, nil
}

func decodeNode(dec *json.Decoder) (node, error) {
	tok, err := dec.Token()
	if err != nil {
		return node{}, err
	}
	switch t := tok.(type) {
	case json.Delim:
		n := node{kind: nodeArray}
		if t == '{' {
			n.kind = nodeObject
		}
		for dec.More() {
			if n.kind == nodeObject {
				k, err := dec.Token()
				if err != nil {
					return node{}, err
				}
				n.keys = append(n.keys, k.(string))
			}
			v, err := decodeNode(dec)
			if err != nil {
				return node{}, err
			}
			n.vals = append(n.vals, v)
		}
		_, err := dec.Token()
		return n, err
	case string:
		return node{scalar: t}, nil
	case json.Number:
		return node{scalar: t.String()}, nil
	case bool:
		return node{scalar: strconv.FormatBool(t)}, nil
	default:
		return node{scalar: "null"}, nil
	}
}

// personAmendment is a person's edit of a draft's text, payload or broadcast, at the rev they saw.
func personAmendment(text *string, payload *json.RawMessage, broadcast *bool, rev *int64) outbox.Amendment {
	a := outbox.Amendment{Text: text, Payload: payload, Broadcast: broadcast, By: draft.Actor{Kind: draft.ActorPerson}}
	if rev != nil {
		a.Rev = *rev
	}
	return a
}

// noteStale remembers that draft id was refused at rev as stale, so its card says so and keeps its approval off
// until it is revised, whatever a later live read within the page's deadline can tell.
func (s *Server) noteStale(id draft.ID, rev int64, err error) {
	if errors.Is(err, outbox.ErrStale) {
		s.refusedStale.Store(id, rev)
	}
}
