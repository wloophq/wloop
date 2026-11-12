package draft

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/jsonschema"
)

// KindSpec is a draft kind as its plugin's manifest declares it, keyed by
// Provider and Name.
type KindSpec struct {
	Provider, Name string
	// Delivery is how the plugin posts a draft of the kind.
	Delivery string
	// Schema and Preview are paths in the plugin's directory: the JSON
	// schema of the kind's payload, and the template of its summary line.
	Schema, Preview string
	// Basis is who states what a draft of the kind was written against:
	// BasisAgent, or BasisNone.
	Basis        string
	TextRequired bool
	// Editable are what a person may change on a draft of the kind: "text",
	// or a JSON pointer into its payload.
	Editable []string
	// Facts are the payload fields an approval rule reads.
	Facts []FactSpec
	// SchemaJSON is the content of Schema.
	SchemaJSON json.RawMessage
	// Posts says a draft of the kind reaches people, as its delivery's
	// posts says.
	Posts bool
}

// ErrEmptyText is wrapped by CheckText.
var ErrEmptyText = errors.New("empty text")

// CheckText refuses a blank text for a kind that requires one.
func (s KindSpec) CheckText(text string) error {
	if s.TextRequired && strings.TrimSpace(text) == "" {
		return fmt.Errorf("%w: a %s draft of %s needs a text", ErrEmptyText, s.Name, s.Provider)
	}
	return nil
}

// The bases a KindSpec names.
const (
	BasisNone  = ""
	BasisAgent = "agent"
)

// FactSpec is a payload field read as an approval fact: Name is
// draft.<provider>.<kind>.<field>, Pointer a JSON pointer into the payload
// to a required string enum.
type FactSpec struct {
	Name, Pointer string
}

// Revision is one version of a draft's content, as its reviser left it.
type Revision struct {
	Rev     int64
	Text    string
	Payload json.RawMessage
	Basis   string
	By      Actor
	At      time.Time
}

// ActorKind is who changed a draft.
type ActorKind string

const (
	ActorPerson  ActorKind = "person"
	ActorSession ActorKind = "session"
	ActorRule    ActorKind = "rule"
	ActorWindow  ActorKind = "window"
)

// Actor is who changed a draft: Session is the agent session of an
// ActorSession, "" otherwise.
type Actor struct {
	Kind    ActorKind
	Session string
}

// DeliverySpec is a delivery a plugin's manifest declares.
type DeliverySpec struct {
	Provider, Name string
	// Posts says a draft of the delivery reaches people.
	Posts bool
}

var (
	// ErrUnknownKind is wrapped when a draft names a kind its provider
	// declares none of.
	ErrUnknownKind = errors.New("unknown draft kind")
	// ErrPayload is wrapped when a payload does not fit its kind's schema.
	ErrPayload = errors.New("the payload does not fit its kind's schema")
)

type kindKey struct{ provider, name string }

// Catalog is the draft kinds and deliveries the plugins' manifests declare,
// each kind keyed by its provider and name.
type Catalog struct {
	kinds      []KindSpec
	deliveries []DeliverySpec
	defaults   map[string]string
	schemas    map[kindKey]*jsonschema.Schema
}

// NewCatalog is the catalog of kinds and deliveries; defaults are, by
// provider, the delivery of a draft that names neither a kind nor one of
// the provider's deliveries. It refuses a kind declared twice by one
// provider, a delivery none of its provider's deliveries names, and a
// schema that does not compile.
func NewCatalog(kinds []KindSpec, deliveries []DeliverySpec, defaults map[string]string) (*Catalog, error) {
	c := &Catalog{deliveries: slices.Clone(deliveries), defaults: map[string]string{}, schemas: map[kindKey]*jsonschema.Schema{}}
	for p, d := range defaults {
		c.defaults[p] = d
	}
	var errs []error
	for _, k := range kinds {
		key := kindKey{k.Provider, k.Name}
		if _, dup := c.schemas[key]; dup {
			errs = append(errs, fmt.Errorf("draft kind %s of %s is declared twice", k.Name, k.Provider))
			continue
		}
		d, ok := c.delivery(k.Provider, k.Delivery)
		if !ok {
			errs = append(errs, fmt.Errorf("draft kind %s of %s: delivery %q is not one of its plugin's", k.Name, k.Provider, k.Delivery))
			continue
		}
		s, err := jsonschema.Compile(k.SchemaJSON, jsonschema.Options{})
		if err != nil {
			errs = append(errs, fmt.Errorf("draft kind %s of %s: %w", k.Name, k.Provider, err))
			continue
		}
		k.Posts = d.Posts
		c.schemas[key] = s
		c.kinds = append(c.kinds, k)
	}
	return c, errors.Join(errs...)
}

func (c *Catalog) delivery(provider, name string) (DeliverySpec, bool) {
	i := slices.IndexFunc(c.deliveries, func(d DeliverySpec) bool { return d.Provider == provider && d.Name == name })
	if i < 0 {
		return DeliverySpec{}, false
	}
	return c.deliveries[i], true
}

// Kinds are the catalog's kinds, in the order they were declared.
func (c *Catalog) Kinds() []KindSpec { return slices.Clone(c.kinds) }

// Lookup is the kind name of provider; false when provider declares none.
func (c *Catalog) Lookup(provider, name string) (KindSpec, bool) {
	i := slices.IndexFunc(c.kinds, func(k KindSpec) bool { return k.Provider == provider && k.Name == name })
	if i < 0 {
		return KindSpec{}, false
	}
	return c.kinds[i], true
}

// Validate refuses a payload that does not fit spec's schema, wrapping
// ErrPayload; an empty payload is the empty object.
func (c *Catalog) Validate(spec KindSpec, payload json.RawMessage) error {
	s, ok := c.schemas[kindKey{spec.Provider, spec.Name}]
	if !ok {
		return fmt.Errorf("%w: %s of %s", ErrUnknownKind, spec.Name, spec.Provider)
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if err := s.ValidateJSON(payload); err != nil {
		return fmt.Errorf("%w: %s of %s: %w", ErrPayload, spec.Name, spec.Provider, err)
	}
	return nil
}

// Posts says whether a draft of provider's delivery reaches people; a
// delivery provider does not declare does, so a rule must name it to
// release it.
func (c *Catalog) Posts(provider, delivery string) bool {
	d, ok := c.delivery(provider, delivery)
	return !ok || d.Posts
}

// Shape is d with its kind, purpose and delivery as provider's manifest
// declares them, provider being the one that posts d. A kind d names is
// looked up; with none, it is the kind named as d's purpose when that kind
// posts as d's delivery, or when d names no delivery or one provider does not
// declare, else the first kind declared with d's delivery, else with
// provider's default delivery. Its purpose is its kind when it names none,
// and its delivery is its kind's. A kind provider does not declare wraps
// ErrUnknownKind. A provider that declares no kind leaves d as it is but
// for an empty purpose, which takes its kind.
func (c *Catalog) Shape(provider string, d Draft) (Draft, error) {
	if !slices.ContainsFunc(c.kinds, func(k KindSpec) bool { return k.Provider == provider }) {
		d.Purpose = cmp.Or(d.Purpose, string(d.Kind))
		return d, nil
	}
	name := string(d.Kind)
	if name == "" {
		name = c.kindOf(provider, d.Purpose, string(d.Delivery))
	}
	spec, ok := c.Lookup(provider, name)
	if !ok {
		return d, fmt.Errorf("%w: %q of %s", ErrUnknownKind, cmp.Or(name, d.Purpose), provider)
	}
	d.Kind, d.Purpose, d.Delivery = Kind(spec.Name), cmp.Or(d.Purpose, spec.Name), Delivery(spec.Delivery)
	return d, nil
}

func (c *Catalog) kindOf(provider, purpose, delivery string) string {
	_, declared := c.delivery(provider, delivery)
	if k, ok := c.Lookup(provider, purpose); ok && (delivery == "" || !declared || k.Delivery == delivery) {
		return k.Name
	}
	if !declared {
		delivery = c.defaults[provider]
	}
	for _, k := range c.kinds {
		if k.Provider == provider && k.Delivery == delivery {
			return k.Name
		}
	}
	return ""
}
