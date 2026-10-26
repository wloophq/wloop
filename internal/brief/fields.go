package brief

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
)

// Fill asks the agent only the extra fields ref's brief lacks at their
// current versions, those of need when need is not empty, and stores the
// brief with them as a fields run reusing the core: the core is never
// briefed again for a new or changed field. It reports whether it filled
// any; a brief at an older core version is left for its refresh. A failed
// call leaves the brief and its record as they were.
func (q *Queue) Fill(ctx context.Context, ref github.Ref, need []string) (bool, error) {
	if !q.d.Enabled || q.d.Schema.Extra == nil {
		return false, nil
	}
	rec, has, err := q.d.Store.BriefRecord(ctx, ref)
	if err != nil || !has {
		return false, err
	}
	b := stored(rec)
	taxonomy, version := q.version(ref)
	if b == nil || rec.Version != version {
		return false, nil
	}
	missing := q.d.Schema.Missing(*b)
	if len(need) > 0 {
		missing = slices.DeleteFunc(missing, func(n string) bool { return !slices.Contains(need, n) })
	}
	if len(missing) == 0 || !q.claim(ref) {
		return false, nil
	}
	defer q.release(ref)
	in, err := q.d.Source.Facts(ctx, ref)
	if err != nil {
		return false, err
	}
	if in.Diff, in.DiffCut, err = q.d.Source.Diff(ctx, ref, q.d.MaxDiffBytes); err != nil {
		return false, err
	}
	start := q.d.Now()
	core, _ := json.Marshal(Brief{Goal: b.Goal, Context: b.Context, Kind: b.Kind, Size: b.Size, Risk: b.Risk, RiskWhy: b.RiskWhy,
		OpenQuestions: b.OpenQuestions, Tags: b.Tags})
	res, err := q.d.Runner.Run(ctx, Call{Tier: q.d.Tier, Home: q.d.Home(ref), Schema: q.d.Schema, Fields: missing,
		Prompt: fill(cmp.Or(q.d.FieldsPrompt, fieldsTemplate), taxonomy, "", in, []string{
			"{{brief}}", string(core), "{{fields}}", q.d.Schema.fieldsText(missing)})})
	if err != nil {
		return false, fmt.Errorf("brief %s: the missing fields %s: %w", ref, strings.Join(missing, ", "), err)
	}
	nb := q.d.Schema.WithFields(*b, res.Fields)
	raw, _ := json.Marshal(nb)
	r := dinv.BriefRun{PR: ref, Trigger: TriggerFields, Version: version, AttemptOf: rec.AttemptOf, InputSHA: rec.InputSHA,
		HeadOid: rec.HeadOid, BodyEditedAt: rec.BodyEditedAt, ChangedFiles: rec.ChangedFiles}
	r.StartedAt, r.EndedAt, r.Outcome = start, q.d.Now(), OutcomeOK
	r.ReusedFrom, r.Brief, r.Tags = rec.Run, string(raw), nb.Tags
	r.Model, r.Tokens, r.CostUSD = res.Model, res.Tokens, res.CostUSD
	q.state.Lock()
	defer q.state.Unlock()
	id, err := q.d.Store.StartBriefRun(ctx, r)
	if err != nil {
		return false, q.storeFault(ctx, err)
	}
	r.ID = id
	if err := q.d.Store.EndBriefRun(ctx, r, rec); err != nil {
		return false, q.storeFault(ctx, err)
	}
	return true, nil
}

// fieldsText lists names with their schemas, as the missing-fields prompt
// names them.
func (c Composed) fieldsText(names []string) string {
	var lines []string
	for _, p := range c.Extra.Properties() {
		if slices.Contains(names, p.Name) {
			lines = append(lines, fmt.Sprintf("- %s: %s", p.Name, p.Source))
		}
	}
	return strings.Join(lines, "\n")
}
