package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/config"
)

// holdDuration is a draft.propose hold: a duration such as 30m, 24h or 2d,
// or the name of a duration in the config slot, read from cfg.
func holdDuration(v string, cfg config.Config) (time.Duration, error) {
	if strings.HasPrefix(v, "config.") {
		d, ok := configFacts(cfg)[v].(time.Duration)
		if !ok {
			return 0, fmt.Errorf("%s is not a duration of the config", v)
		}
		return d, nil
	}
	if n, ok := strings.CutSuffix(v, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days < 0 {
			return 0, fmt.Errorf("%q is not a duration such as 30m, 24h or 2d", v)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%q is not a duration such as 30m, 24h or 2d", v)
	}
	return d, nil
}
