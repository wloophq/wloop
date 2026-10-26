package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/duration"
)

// repeatDuration is a draft.propose repeat delay: a duration such as 30m,
// 24h or 2d, or the name of a duration in the config slot, read from cfg.
func repeatDuration(v string, cfg config.Config) (time.Duration, error) {
	if strings.HasPrefix(v, "config.") {
		d, ok := configFacts(cfg)[v].(time.Duration)
		if !ok {
			return 0, fmt.Errorf("%s is not a duration of the config", v)
		}
		return d, nil
	}
	return duration.Parse(v)
}
