package workflow

import (
	"encoding/json"
	"slices"
	"strings"
)

// The bounds of a chain of workflows triggering each other.
const (
	DefaultChainDepth = 3
	MaxChainDepth     = 5
)

// Chain is the chain of workflows an event closes: Depth is how many
// workflows ran one after the other to cause it, Workflows their names in
// order. A person's event starts none: its chain is zero.
type Chain struct {
	Depth     int      `json:"depth"`
	Workflows []string `json:"workflows,omitempty"`
}

// Then is c continued by workflow w.
func (c Chain) Then(w string) Chain {
	return Chain{Depth: c.Depth + 1, Workflows: append(slices.Clip(c.Workflows), w)}
}

// Has says whether workflow w is in c already.
func (c Chain) Has(w string) bool { return slices.Contains(c.Workflows, w) }

// chained is an event's data with its chain, as the events table keeps it.
type chained struct {
	Chain Chain  `json:"chain"`
	Data  string `json:"data"`
}

const chainedPrefix = `{"chain":`

// ChainData is the stored data of an event that carries chain c: data
// itself for a zero chain.
func ChainData(data string, c Chain) string {
	if c.Depth == 0 {
		return data
	}
	b, _ := json.Marshal(chained{Chain: c, Data: data})
	return string(b)
}

// SplitChain reads stored event data as ChainData wrote it: the data and
// its chain, zero for data written with none.
func SplitChain(stored string) (string, Chain) {
	if !strings.HasPrefix(stored, chainedPrefix) {
		return stored, Chain{}
	}
	var c chained
	if json.Unmarshal([]byte(stored), &c) != nil {
		return stored, Chain{}
	}
	return c.Data, c.Chain
}

// ActorKind is the kind of actor an event's actor names, as
// "<kind>:<source>" writes it: "person" of "person:cockpit"; "" for an
// actor written otherwise.
func ActorKind(actor string) string {
	kind, _, ok := strings.Cut(actor, ":")
	if !ok {
		return ""
	}
	return kind
}
