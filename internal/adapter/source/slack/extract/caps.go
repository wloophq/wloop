package extract

import (
	"context"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
)

// day is the spend day of now, in the local zone.
func day(now time.Time) string { return now.Local().Format(time.DateOnly) }

// capped names the daily cap that stops further calls today, "" while under both.
func (j *Job) capped(ctx context.Context, ec config.SlackExtraction, now time.Time) (string, error) {
	calls, cost, err := j.Store.DB.Day(ctx, day(now))
	switch {
	case err != nil:
		return "", err
	case calls >= ec.MaxDayCalls():
		return fmt.Sprintf("slack extraction: the daily cap of %d calls is reached", ec.MaxDayCalls()), nil
	case cost >= ec.MaxDayUSD():
		return fmt.Sprintf("slack extraction: the daily cap of $%.2f is reached", ec.MaxDayUSD()), nil
	}
	return "", nil
}

// spend counts a call toward today; an answer without a cost counts as the call cap.
func (j *Job) spend(ctx context.Context, ec config.SlackExtraction, now time.Time, a turns.Answer) error {
	cost := ec.MaxCallUSD()
	if a.CostUSD != nil {
		cost = *a.CostUSD
	}
	return j.Store.DB.Spend(ctx, day(now), cost)
}
