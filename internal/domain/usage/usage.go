// Package usage holds the token and cost records of an agent's turns.
package usage

import "time"

type Tokens struct{ Input, Output, CacheRead, CacheWrite int64 }

func (t Tokens) Plus(o Tokens) Tokens {
	return Tokens{
		Input:      t.Input + o.Input,
		Output:     t.Output + o.Output,
		CacheRead:  t.CacheRead + o.CacheRead,
		CacheWrite: t.CacheWrite + o.CacheWrite,
	}
}

type Model struct {
	Model       string
	Messages    int
	Tokens      Tokens
	CostUSD     *float64
	First, Last time.Time
}
