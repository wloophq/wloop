package eval

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

type SplitScore struct {
	N               int                `json:"n"`
	Expected        int                `json:"expected"`
	SameActions     float64            `json:"same_actions"`
	CriticalOrMajor int                `json:"critical_or_major"`
	Recall          map[string]float64 `json:"recall"`
	Contradictions  int                `json:"contradictions"`
	Extras          int                `json:"extras"`
	Causes          map[string]int     `json:"causes"`
	CostUSD         float64            `json:"cost_usd"`
}

// Score is by split, "dev" and "holdout".
type Score map[string]SplitScore

var recallSections = []string{"my_actions", "my_decisions", "chase"}

// ScoreSplit scores the judgments present of a split of expected fixtures;
// a missing one counts as a critical failure with different actions.
func ScoreSplit(js []Judgment, expected int) SplitScore {
	s := SplitScore{N: len(js), Expected: expected, Recall: map[string]float64{}, Causes: map[string]int{}}
	same := 0
	for _, j := range js {
		if j.SameActions {
			same++
		}
		if j.Severity == "critical" || j.Severity == "major" {
			s.CriticalOrMajor++
		}
		for _, sec := range j.Sections {
			s.Contradictions += len(sec.Contradicted)
			s.Extras += len(sec.Extra)
		}
		for _, c := range j.LedgerRootCauses {
			s.Causes[c.Cause]++
		}
	}
	if missing := expected - len(js); missing > 0 {
		s.CriticalOrMajor += missing
	}
	if expected > 0 {
		s.SameActions = round(float64(same)/float64(expected), 3)
	}
	for _, k := range recallSections {
		matched, ref := 0, 0
		for _, j := range js {
			matched += j.Sections[k].Matched
			ref += j.Sections[k].ReferenceCount
		}
		s.Recall[k] = 1.0
		if ref > 0 {
			s.Recall[k] = round(float64(matched)/float64(ref), 2)
		}
	}
	for _, c := range Causes {
		s.Causes[c] += 0
	}
	return s
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

// WriteScore scores label's judgments under out against the run's manifest
// and writes <out>/<label>/score.json.
func WriteScore(out, label string) (Score, error) {
	var m manifest
	if err := readJSON(manifestPath(out, label), &m); err != nil {
		return nil, fmt.Errorf("no run of %s under %s: %w", label, out, err)
	}
	score := Score{}
	for _, split := range []string{SplitDev, SplitHoldout} {
		var js []Judgment
		expected, cost := 0, 0.0
		for _, e := range m.Fixtures {
			if e.Split != split {
				continue
			}
			expected++
			for _, p := range []string{filepath.Join(out, "oracle"), filepath.Join(out, label, "ledger"), filepath.Join(out, label, "blind"), filepath.Join(out, label, "judge")} {
				cost += stageCost(filepath.Join(p, e.ID+".json"))
			}
			var j Judgment
			err := readJSON(filepath.Join(out, label, "judge", e.ID+".json"), &j)
			switch {
			case errors.Is(err, os.ErrNotExist):
			case err != nil:
				return nil, err
			default:
				js = append(js, j)
			}
		}
		if expected == 0 {
			continue
		}
		s := ScoreSplit(js, expected)
		s.CostUSD = round(cost, 4)
		score[split] = s
	}
	return score, writeJSON(filepath.Join(out, label, "score.json"), score)
}

// Bar fails unless the holdout is complete, has no critical or major
// failure, and reaches bar.
func (s Score) Bar(bar float64) error {
	h, ok := s[SplitHoldout]
	switch {
	case !ok || h.Expected == 0:
		return errors.New("bar: no holdout fixture")
	case h.N < h.Expected:
		return fmt.Errorf("bar: the holdout is incomplete, %d of %d judged", h.N, h.Expected)
	case h.CriticalOrMajor > 0:
		return fmt.Errorf("bar: %d holdout judgments are critical or major", h.CriticalOrMajor)
	case h.SameActions < bar:
		return fmt.Errorf("bar: holdout same_actions %.3f is below %.3f", h.SameActions, bar)
	}
	return nil
}
