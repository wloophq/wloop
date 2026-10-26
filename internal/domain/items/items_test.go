package items

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var thread = unit.Unit{Provider: "slack", Key: "C000AAA/1.2"}

// foldCodec is a provider whose keys compare without case, as a codec with
// Equal says.
type foldCodec struct{ id unit.ProviderID }

func (c foldCodec) Provider() unit.ProviderID { return c.id }
func (foldCodec) Valid(string) error          { return nil }
func (foldCodec) Equal(a, b string) bool      { return strings.EqualFold(a, b) }

func TestPersonKeysCompareThroughTheirCodec(t *testing.T) {
	codecs := unit.NewRegistry(foldCodec{"forge"}, unit.OpaqueCodec{ID: "chat"})
	me := Identity{Keys: []string{"forge:Octocat", "chat:U000AAA", "email:me@example.com"}, Codecs: codecs}
	for _, p := range []Person{
		{Key: "forge:octocat"},
		{Key: "chat:U000AAA"},
		{Key: "chat:U000BBB", Aliases: []string{"email:me@example.com"}},
		{Key: "group:x", Aliases: []string{"FORGE:OCTOCAT"}},
	} {
		if !p.Is(me) {
			t.Errorf("%+v is not me", p)
		}
	}
	for _, p := range []Person{
		{Key: "chat:u000aaa"},
		{Key: "email:ME@example.com"},
		{Key: "forge:someone"},
		{Key: "chat:Octocat"},
		{},
	} {
		if p.Is(me) {
			t.Errorf("%+v is me", p)
		}
	}
	if (Person{Key: "forge:octocat"}).Is(Identity{Keys: me.Keys}) {
		t.Error("without codecs, keys compare byte for byte")
	}
}

func ptr[T any](v T) *T { return &v }

func TestValidateShape(t *testing.T) {
	at := unit.Anchor{Ref: "C000AAA/1.3"}
	ok := []Op{
		{Op: "create", Unit: thread, Anchor: at, Fields: &Fields{Kind: ptr(Kind("request")), Summary: ptr("do x")}},
		{Op: "update", Unit: thread, Item: "i1", Anchor: at, Fields: &Fields{Summary: ptr("y")}},
		{Op: "assign", Unit: thread, Item: "i1", Anchor: at, Assignees: []Person{{Key: "slack:U000AAA"}}},
		{Op: "resolve", Unit: thread, Item: "i1", Anchor: at, Resolution: "done"},
		{Op: "reopen", Unit: thread, Item: "i1", Anchor: at},
		{Op: "link", Unit: thread, Item: "i1", Anchor: at, To: &unit.Unit{Provider: unit.GitHub, Key: "o/r#1"}, LinkKind: link.Review},
		{Op: "evidence", Unit: thread, Item: "i1", Anchor: at, Evidence: &Evidence{Kind: "sent_draft", Anchor: unit.Anchor{Ref: "draft:7:reply"}}},
	}
	for _, o := range ok {
		if err := o.ValidateShape(); err != nil {
			t.Errorf("%s refused: %v", o.Op, err)
		}
	}
	bad := map[string]Op{
		"anchor: required":    {Op: "reopen", Unit: thread, Item: "i1"},
		"op: unknown":         {Op: "close", Unit: thread, Item: "i1", Anchor: at},
		"unit: required":      {Op: "reopen", Item: "i1", Anchor: at},
		"item: required":      {Op: "reopen", Unit: thread, Anchor: at},
		"fields.kind":         {Op: "create", Unit: thread, Anchor: at, Fields: &Fields{Summary: ptr("x")}},
		"fields.summary":      {Op: "create", Unit: thread, Anchor: at, Fields: &Fields{Kind: ptr(Kind("todo"))}},
		"fields: required":    {Op: "update", Unit: thread, Item: "i1", Anchor: at},
		"evidence: required":  {Op: "evidence", Unit: thread, Item: "i1", Anchor: at},
		"to: required":        {Op: "link", Unit: thread, Item: "i1", Anchor: at, LinkKind: link.Review},
		"link_kind":           {Op: "link", Unit: thread, Item: "i1", Anchor: at, To: &unit.Unit{Provider: unit.GitHub, Key: "o/r#1"}, LinkKind: "closes"},
		"resolution":          {Op: "resolve", Unit: thread, Item: "i1", Anchor: at, Resolution: "nope"},
		"assignees":           {Op: "assign", Unit: thread, Item: "i1", Anchor: at},
		"fields.summary: 301": {Op: "update", Unit: thread, Item: "i1", Anchor: at, Fields: &Fields{Summary: ptr(strings.Repeat("x", 301))}},
	}
	for want, o := range bad {
		err := o.ValidateShape()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}
