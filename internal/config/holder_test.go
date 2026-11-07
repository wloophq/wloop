package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type holderRig struct {
	t     *testing.T
	file  string
	now   time.Time
	loads int
	stats int
	notes []string
	flips int
	h     *Holder
}

func newHolderRig(t *testing.T) *holderRig {
	t.Helper()
	r := &holderRig{t: t, file: filepath.Join(t.TempDir(), "config.toml"), now: time.Unix(1000, 0)}
	r.write(`sweep = {fast = "2m"}`)
	cfg, err := LoadFile(r.file, tools())
	if err != nil {
		t.Fatal(err)
	}
	r.h = NewHolder(cfg, HolderOptions{
		Path:            r.file,
		Stat:            func(p string) (os.FileInfo, error) { r.stats++; return os.Stat(p) },
		Load:            func() (Config, error) { r.loads++; return LoadFile(r.file, tools()) },
		Now:             func() time.Time { return r.now },
		OnProfileChange: func(p string) { r.notes = append(r.notes, p) },
		OnWarningChange: func() { r.flips++ },
	})
	r.stats = 0
	return r
}

func (r *holderRig) write(body string) {
	r.t.Helper()
	if err := os.WriteFile(r.file, []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
	r.now = r.now.Add(time.Second)
	if err := os.Chtimes(r.file, r.now, r.now); err != nil {
		r.t.Fatal(err)
	}
}

func (r *holderRig) later() { r.now = r.now.Add(StatEvery) }

func TestHolderIsStaleAfterAWrite(t *testing.T) {
	r := newHolderRig(t)
	if r.h.Stale() {
		t.Fatal("stale before any write")
	}
	r.write(`sweep = {fast = "5m"}`)
	r.later()
	if !r.h.Stale() {
		t.Fatal("a job interval edit left the holder fresh")
	}
	if got := r.h.Config().Sweep.Fast; got != Days(2*time.Minute) {
		t.Fatalf("the running interval changed to %v", got)
	}
}

func TestHolderStatsAtMostEveryStaleEvery(t *testing.T) {
	r := newHolderRig(t)
	r.write(`sweep = {fast = "5m"}`)
	for range 100 {
		r.h.Config()
		r.h.Stale()
	}
	if r.stats != 0 || r.loads != 0 {
		t.Fatalf("%d stats and %d loads inside the window", r.stats, r.loads)
	}
	r.later()
	r.h.Stale()
	r.h.Stale()
	if r.stats != 1 || r.loads != 1 {
		t.Fatalf("%d stats and %d loads after the window, want 1 and 1", r.stats, r.loads)
	}
}

func TestHolderFollowsTheProfileAndNotesIt(t *testing.T) {
	r := newHolderRig(t)
	r.write(`profile = "/tmp/p"

[sweep]
fast = "2m"
`)
	r.later()
	if got := r.h.Config().Profile; got != "/tmp/p" {
		t.Fatalf("profile = %q", got)
	}
	if r.h.Stale() {
		t.Fatal("a profile edit marked the holder stale")
	}
	if len(r.notes) != 1 || r.notes[0] != "/tmp/p" {
		t.Fatalf("notes = %v", r.notes)
	}
}

func TestHolderKeepsTheLastGoodConfigWhenTheFileBreaks(t *testing.T) {
	r := newHolderRig(t)
	r.write(`{`)
	r.later()
	if r.h.Stale() || r.h.Config().Sweep.Fast != Days(2*time.Minute) {
		t.Fatal("a broken file changed the holder")
	}
}

func TestHolderWarnsWhileTheFileIsBrokenAndClearsWhenItLoads(t *testing.T) {
	r := newHolderRig(t)
	r.write(`{`)
	r.later()
	if w := r.h.Warning(); w.Key == "" {
		t.Fatal("a broken file raised no warning")
	}
	r.write(`sweep = {fast = "2m"}`)
	r.later()
	if w := r.h.Warning(); w.Key != "" {
		t.Fatalf("warning after the file loads: %q", w)
	}
}

func TestHolderBreakAnswersUntilRebase(t *testing.T) {
	r := newHolderRig(t)
	if r.h.Err() != nil {
		t.Fatal("a fresh holder is broken")
	}
	r.h.Break(os.ErrInvalid)
	if r.h.Err() != os.ErrInvalid {
		t.Fatalf("Err = %v", r.h.Err())
	}
	r.h.Rebase(Defaults())
	if r.h.Err() != nil {
		t.Fatal("Rebase did not clear the failure")
	}
}

func TestHolderCallsOnWarningChangeWhenTheWarningFlips(t *testing.T) {
	r := newHolderRig(t)
	r.write(`sweep = {fast = "5m"}`)
	r.later()
	r.h.Config()
	if r.flips != 1 {
		t.Fatalf("%d flips after a stale write, want 1", r.flips)
	}
	r.write(`sweep = {fast = "6m"}`)
	r.later()
	r.h.Config()
	if r.flips != 1 {
		t.Fatalf("%d flips for a warning that did not change, want 1", r.flips)
	}
	r.write(`{`)
	r.later()
	r.h.Config()
	if r.flips != 2 {
		t.Fatalf("%d flips after the file broke, want 2", r.flips)
	}
}

func TestUILocaleAppliesWithoutARestart(t *testing.T) {
	r := newHolderRig(t)
	r.write("sweep = {fast = \"2m\"}\nui = {locale = \"pt-BR\"}\n")
	r.later()
	if got := r.h.Config().UI.Locale; got != "pt-BR" || r.h.Stale() || r.h.Warning().Key != "" {
		t.Fatalf("locale %q, stale %v, warning %q", got, r.h.Stale(), r.h.Warning())
	}
	r.write("sweep = {fast = \"5m\"}\nui = {locale = \"en\"}\n")
	r.later()
	if got := r.h.Config().UI.Locale; got != "en" || !r.h.Stale() {
		t.Fatalf("locale %q, stale %v", got, r.h.Stale())
	}
	if w := r.h.Warning(); w.Key != WarningStale || !strings.Contains(fmt.Sprint(w.Params["keys"]), "ui.locale") || !strings.Contains(fmt.Sprint(w.Params["keys"]), "profile") {
		t.Fatalf("warning %q", w)
	}
}

func TestAWorkflowSwitchAppliesWithoutARestart(t *testing.T) {
	r := newHolderRig(t)
	r.write("sweep = {fast = \"2m\"}\n[workflows.wf]\nenabled = false\n")
	r.h.Reload()
	if w := r.h.Config().Workflows["wf"]; w.Enabled == nil || *w.Enabled || r.h.Stale() {
		t.Fatalf("workflow %+v, stale %v", w, r.h.Stale())
	}
	r.write("sweep = {fast = \"2m\"}\n[workflows.wf]\nenabled = false\nobserve = true\n")
	r.h.Reload()
	if w := r.h.Config().Workflows["wf"]; w.Enabled == nil || *w.Enabled || w.Observe != nil || !r.h.Stale() {
		t.Fatalf("observe beside the switch: workflow %+v, stale %v", w, r.h.Stale())
	}
}

func TestAWorkflowSwitchDoesNotTouchTheStartConfig(t *testing.T) {
	r := newHolderRig(t)
	r.write("sweep = {fast = \"2m\"}\n[workflows.wf]\nenabled = false\n")
	r.h.Reload()
	r.write("sweep = {fast = \"2m\"}\n")
	r.h.Reload()
	if r.h.Stale() || r.h.Warning().Key != "" {
		t.Fatalf("back to the start file: stale %v, warning %q", r.h.Stale(), r.h.Warning())
	}
}

func TestAnotherChangeStillNeedsARestart(t *testing.T) {
	r := newHolderRig(t)
	r.write(`sweep = {fast = "5m"}`)
	r.h.Reload()
	if !r.h.Stale() || r.h.Config().Sweep.Fast != Days(2*time.Minute) {
		t.Fatalf("stale %v, fast %v", r.h.Stale(), r.h.Config().Sweep.Fast)
	}
}

func TestReloadReadsTheFileAtOnce(t *testing.T) {
	r := newHolderRig(t)
	r.write("sweep = {fast = \"2m\"}\nui = {locale = \"pt-BR\"}\n")
	loads := r.loads
	r.h.Reload()
	if r.loads != loads+1 || r.h.Config().UI.Locale != "pt-BR" {
		t.Fatalf("%d loads, locale %q", r.loads-loads, r.h.Config().UI.Locale)
	}
}

func TestConfigWarningIsACodeNotEnglish(t *testing.T) {
	r := newHolderRig(t)
	r.write("sweep = {fast = \"5m\"}\n")
	r.later()
	if w := r.h.Warning(); w.Key != WarningStale || !strings.HasPrefix(fmt.Sprint(w.Params["keys"]), "profile, ui.locale") {
		t.Fatalf("%+v", w)
	}
	r.write("sweep = 3\n")
	r.later()
	if w := r.h.Warning(); w.Key != WarningBroken || w.Params["error"] == "" {
		t.Fatalf("%+v", w)
	}
}
