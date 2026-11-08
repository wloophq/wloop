package workflow

import (
	"regexp"
	"strings"
)

// The reasons the engine gives a plan item, as templates whose {name} is a value the reason carries. Reason
// renders one for the ledger and the terminal; the cockpit words it through the catalog, whose key is the
// template, after ReasonOf reads it back.
const (
	ReasonWaits       = "waits for you"
	ReasonVisible     = "a visible step waits for you"
	ReasonObserve     = "observe mode"
	ReasonWindow      = "runs when its window ends"
	ReasonGuided      = "guided by you"
	ReasonPaused      = "paused"
	ReasonHeld        = "waits for you while {facts}"
	ReasonHeldRefused = "waits for you while {facts}: {refusal}"
	ReasonRetrying    = "retrying after {error}"
	ReasonFailed      = "failed {n} times: {error}"
	ReasonPerRun      = "limit per_run {n} reached"
	ReasonPerDay      = "limit per_day {n} reached"
	ReasonPerUnitDay  = "limit per_unit_per_day {n} reached"
	ReasonCooling     = "cooling down for {duration}"

	ReasonSincePaused    = "paused since it was planned"
	ReasonSinceUnmatched = "it no longer matches since it was planned"
	ReasonSinceUnit      = "its unit changed since it was planned"
	ReasonSinceWaits     = "it waits for you since it was planned"
	ReasonSinceSource    = "its source moved since it was planned"
	ReasonSinceLeft      = "it left the plan since it was planned"
	ReasonSinceActed     = "it was acted on since it was planned"
	ReasonSinceDone      = "it was done since it was planned"
	ReasonSinceDismissed = "it was dismissed since it was planned"
)

// Reasons are the engine's reason templates, those carrying a refusal after those they extend, so ReasonOf
// tries the longer first.
var Reasons = []string{
	ReasonWaits, ReasonVisible, ReasonObserve, ReasonWindow, ReasonGuided, ReasonPaused,
	ReasonHeldRefused, ReasonHeld, ReasonRetrying, ReasonFailed, ReasonPerRun, ReasonPerDay, ReasonPerUnitDay, ReasonCooling,
	ReasonSincePaused, ReasonSinceUnmatched, ReasonSinceUnit, ReasonSinceWaits, ReasonSinceSource, ReasonSinceLeft,
	ReasonSinceActed, ReasonSinceDone, ReasonSinceDismissed,
}

var reasonParam = regexp.MustCompile(`\{([a-z]+)\}`)

// Reason is template key with each {name} replaced by its value in params.
func Reason(key string, params map[string]string) string {
	return reasonParam.ReplaceAllStringFunc(key, func(m string) string { return params[m[1:len(m)-1]] })
}

var reasonPatterns = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(Reasons))
	for i, k := range Reasons {
		parts := reasonParam.Split(k, -1)
		names := reasonParam.FindAllStringSubmatch(k, -1)
		var b strings.Builder
		b.WriteString("^")
		for j, p := range parts {
			b.WriteString(regexp.QuoteMeta(p))
			if j < len(names) {
				b.WriteString("(?P<" + names[j][1] + ">.+?)")
			}
		}
		b.WriteString("$")
		out[i] = regexp.MustCompile(b.String())
	}
	return out
}()

// ReasonOf is the template text renders and the values it carries; false when no template renders it, as a
// refusal a step or a guard words itself.
func ReasonOf(text string) (string, map[string]string, bool) {
	for i, re := range reasonPatterns {
		m := re.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		var params map[string]string
		for j, name := range re.SubexpNames() {
			if name == "" {
				continue
			}
			if params == nil {
				params = map[string]string{}
			}
			params[name] = m[j]
		}
		return Reasons[i], params, true
	}
	return "", nil, false
}
