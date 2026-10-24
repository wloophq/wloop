package port

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/unit"
)

type Heat string

const (
	HeatActive Heat = "active"
	HeatWarm   Heat = "warm"
	HeatCold   Heat = "cold"
)

type HeatReader interface {
	Heat(ctx context.Context, units []unit.Unit) (map[unit.Unit]Heat, error) // a unit missing from the map is cold
}
