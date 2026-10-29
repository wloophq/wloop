package web

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// servedCatalog is GET /api/catalog as the daemon serves the GitHub
// provider's lists.
func servedCatalog(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../readmodel/prrow/testdata/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// catalogFuncs are the functions that build the lens's fields from the
// catalog and read a row's facets.
var catalogFuncs = []string{"applyCatalog", "kindLabel", "rowKindLabel", "fieldOf", "facetValues", "bucketsOf", "facetTime", "goDuration", "bucketOf", "dueNow",
	"pl", "sm", "fieldRank", "fieldShow", "exploreSearch", "reviewDecision"}

// catalogPrelude declares the catalog and the functions it needs, and
// applies the served catalog.
func catalogPrelude(t *testing.T) string {
	src := appJS(t)
	var b strings.Builder
	b.WriteString(jsConst(t, src, "catalog"))
	b.WriteString(jsBlock(t, src, "MATCH"))
	for _, f := range catalogFuncs {
		b.WriteString(jsFunction(t, src, f))
	}
	b.WriteString("\napplyCatalog(" + servedCatalog(t) + ");\n")
	return b.String()
}

// wireRows turns test rows written with every field at the top into rows as
// the daemon serves them: the pull request's fields in the
// payload, the situation in the summary, and the facets the GitHub provider
// derives for viewer at now.
func wireRows(viewer string, now time.Time, rows ...map[string]any) []map[string]any {
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		out[i] = wireRow(viewer, now, r)
	}
	return out
}

var payloadKeys = []string{"author", "repo", "head", "approved", "reasons", "workspace", "override", "brief", "intake", "facts"}

func wireRow(viewer string, now time.Time, old map[string]any) map[string]any {
	r := map[string]any{}
	payload := map[string]any{}
	for k, v := range old {
		switch {
		case k == "ref":
			r["ref"] = v
			ref := v.(map[string]any)
			label := str(ref["owner"]) + "/" + str(ref["repo"]) + "#" + fmt.Sprint(ref["number"])
			if old["label"] == nil {
				r["label"] = label
			}
			if old["unit"] == nil {
				r["unit"] = map[string]any{"provider": "github", "key": label}
			}
		case slices.Contains(payloadKeys, k):
			payload[k] = v
		case k == "waiting_on" || k == "since" || k == "stalled" || k == "reason":
		default:
			r[k] = v
		}
	}
	if old["unit"] != nil {
		r["unit"] = old["unit"]
	}
	if len(payload) > 0 {
		payload["viewer"] = viewer
		r["payload"] = payload
	}
	kind := str(old["kind"])
	r["summary"] = map[string]any{"waits_on": str(old["waiting_on"]), "since": cmpOr(str(old["since"]), "0001-01-01T00:00:00Z"), "stalled": old["stalled"] == true,
		"closed": kind == "done", "reason": str(old["reason"]), "line": str(old["reason"])}
	r["facets"] = facetsOf(viewer, now, old)
	return r
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func strs(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			out = append(out, str(e))
		}
		return out
	}
	return nil
}

func isSetTime(s string) bool { return s != "" && !strings.HasPrefix(s, "0001-01-01") }

// facetsOf mirrors the GitHub provider's facets for a test row.
func facetsOf(viewer string, now time.Time, r map[string]any) map[string][]string {
	author, kind := str(r["author"]), str(r["kind"])
	views := strs(r["views"])
	brief, ws, o, facts := obj(r["brief"]), obj(r["workspace"]), obj(r["override"]), obj(r["facts"])
	mine := viewer != "" && strings.EqualFold(author, viewer)
	resolve := func(w string) string {
		if w != "author" {
			return w
		}
		if mine {
			return "me"
		}
		return author
	}
	var who []string
	cats := map[string]bool{}
	for _, w := range strings.Split(str(r["waiting_on"]), ", ") {
		if w == "" || w == "nobody" {
			continue
		}
		who = append(who, resolve(w))
		switch {
		case resolve(w) == "me":
			cats["me"] = true
		case w == "author":
			cats["author"] = true
		default:
			cats["reviewer"] = true
		}
	}
	var wait []string
	if kind == "loop_working" || kind == "loop_waiting" {
		wait = []string{"loop"}
	} else {
		for _, c := range []string{"me", "loop", "reviewer", "author", "nobody"} {
			if cats[c] {
				wait = append(wait, c)
			}
		}
		if len(wait) == 0 {
			wait = []string{"nobody"}
		}
	}
	f := map[string][]string{"kind": {kind}, "wait": wait, "waits-on": who}
	if repo := str(r["repo"]); repo != "" {
		f["repo"] = []string{repo}
	}
	if author != "" {
		f["author"] = []string{author}
		if mine {
			f["author"] = []string{"me", author}
		}
	}
	f["type"] = []string{cmpOr(str(brief["kind"]), "none")}
	f["risk"] = []string{oneOf(str(brief["risk"]), "low", "medium", "high")}
	f["size"] = []string{oneOf(str(brief["size"]), "xs", "s", "m", "l", "xl")}
	f["status"] = []string{cmpOr(str(ws["status"]), "none")}
	f["tag"] = strs(brief["tags"])
	mute := obj(r["mute"])
	mutedUntil, _ := time.Parse(time.RFC3339, str(mute["until"]))
	flags := map[string]bool{"stalled": slices.Contains(views, "stalled"), "needs-me": slices.Contains(views, "needs-me"), "in-loop": slices.Contains(views, "loop"),
		"approved": r["approved"] == true, "hold": o["hold"] == true, "muted": mute["until_state_change"] == true || mutedUntil.After(now),
		"adopted": o["adopted"] == true, "tracked": o["tracked"] == true}
	f["is"] = []string{}
	for _, k := range []string{"stalled", "needs-me", "in-loop", "approved", "hold", "muted", "adopted", "tracked"} {
		if flags[k] {
			f["is"] = append(f["is"], k)
		}
	}
	checks := obj(facts["checks"])
	f["ci"] = append([]string{cmpOr(strings.ToLower(str(checks["state"])), "none")}, strs(checks["failing"])...)
	if c := str(facts["created_at"]); isSetTime(c) {
		f["opened"] = []string{c}
	}
	f["scope"] = strs(r["reasons"])
	if s := str(r["since"]); isSetTime(s) {
		f["age"] = []string{s}
	}
	var due []string
	for _, n := range anys(facts["next"]) {
		if at := str(obj(n)["at"]); isSetTime(at) {
			due = append(due, at)
		}
	}
	if p := obj(r["pending"]); isSetTime(str(p["deadline"])) {
		due = append(due, str(p["deadline"]))
	}
	if len(due) > 0 {
		f["due"] = due
	}
	lane := "author"
	switch {
	case slices.Contains(views, "needs-me"):
		lane = "me"
	case slices.Contains(views, "loop"):
		lane = "loop"
	case kind == "merge":
		lane = "merge"
	case kind == "review" || kind == "reviewer_back":
		lane = "reviewer"
	case slices.Contains(who, "me"):
		lane = "me"
	}
	f["lane"] = []string{lane}
	return f
}

func anys(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []map[string]any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out
	}
	return nil
}

func oneOf(v string, allowed ...string) string {
	if slices.Contains(allowed, v) {
		return v
	}
	return "none"
}
