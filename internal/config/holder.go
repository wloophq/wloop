package config

import (
	"fmt"
	"maps"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"
)

// StatEvery is how often a Holder looks at the config file.
const StatEvery = 2 * time.Second

// HolderOptions are the collaborators of a Holder.
type HolderOptions struct {
	// Path is the config file to watch; "" watches nothing.
	Path string
	// Load reads the config; nil disables reloads.
	Load func() (Config, error)
	// Stat is os.Stat when nil.
	Stat func(string) (os.FileInfo, error)
	// Every is the stat interval; StatEvery when zero.
	Every time.Duration
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// OnProfileChange runs, outside the lock, after the profile setting changed to profile.
	OnProfileChange func(profile string)
	// OnWarningChange runs, outside the lock, after a reload changed what Warning answers.
	OnWarningChange func()
}

type fileSig struct {
	modTime time.Time
	size    int64
	exists  bool
}

// liveKey is a setting a changed file applies at once: take copies its value from the file's config.
type liveKey struct {
	name string
	take func(running *Config, file Config)
}

// liveKeys are the settings that follow the file while a serve runs.
var liveKeys = []liveKey{
	{"profile", func(r *Config, f Config) { r.Profile = f.Profile }},
	{"ui.locale", func(r *Config, f Config) { r.UI.Locale = f.UI.Locale }},
	{"workflows.<name>.enabled", takeWorkflowSwitches},
}

// takeWorkflowSwitches sets each workflow's enabled in r to f's, on a copy of r's workflows, since the
// running and start configs share theirs; a workflow left with no setting drops out.
func takeWorkflowSwitches(r *Config, f Config) {
	out := maps.Clone(r.Workflows)
	for name := range f.Workflows {
		if _, ok := out[name]; !ok {
			if out == nil {
				out = map[string]WorkflowConfig{}
			}
			out[name] = WorkflowConfig{}
		}
	}
	for name, w := range out {
		w.Enabled = f.Workflows[name].Enabled
		if reflect.ValueOf(w).IsZero() {
			delete(out, name)
			continue
		}
		out[name] = w
	}
	if len(out) == 0 {
		out = nil
	}
	r.Workflows = out
}

// Holder is the one configuration a serve holds. Config is the config the
// serve started with, except that the live keys follow the file; every
// other change makes Stale true until the serve restarts.
type Holder struct {
	opts HolderOptions

	mu      sync.Mutex
	start   Config
	live    Config
	stale   bool
	broken  error
	badFile error
	sig     fileSig
	checked time.Time
}

// NewHolder holds cfg, the config just loaded from opts.Path.
func NewHolder(cfg Config, opts HolderOptions) *Holder {
	if opts.Every == 0 {
		opts.Every = StatEvery
	}
	if opts.Stat == nil {
		opts.Stat = os.Stat
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	h := &Holder{opts: opts}
	h.rebase(cfg)
	return h
}

// Rebase makes cfg, just loaded, the config the serve runs with.
func (h *Holder) Rebase(cfg Config) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rebase(cfg)
}

func (h *Holder) rebase(cfg Config) {
	h.start, h.live, h.stale, h.broken, h.badFile = cfg, cfg, false, nil, nil
	h.sig, h.checked = h.statFile(), h.opts.Now()
}

// Break records that no config has loaded yet, so Err answers err until Rebase.
func (h *Holder) Break(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.broken = err
}

// Err is the failure recorded by Break, nil once a config has loaded.
func (h *Holder) Err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.broken
}

// Config is the config the serve runs with, its live keys as the file now sets them.
func (h *Holder) Config() Config {
	h.refresh()
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.live
}

// Warning is what a restart or a fix of config.toml would change: the load
// failure of a file that broke, else that a changed file needs a restart; none
// when the running config matches the file.
func (h *Holder) Warning() Warning {
	h.refresh()
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.warning()
}

// Warning is what a restart or a fix of config.toml would change, as a key of the client strings
// (strings.toml) and its {name} params; Key is "" when there is nothing to say.
type Warning struct {
	Key    string
	Params map[string]any
}

// The keys of the config warnings, which strings.toml words.
const (
	WarningBroken = "config.toml does not load, so the running config stays: {error}"
	WarningStale  = "config.toml changed; only these settings apply live: {keys}. Restart the daemon to apply the rest"
)

func (w Warning) same(o Warning) bool {
	return w.Key == o.Key && fmt.Sprint(w.Params) == fmt.Sprint(o.Params)
}

func (h *Holder) warning() Warning {
	switch {
	case h.badFile != nil:
		return Warning{Key: WarningBroken, Params: map[string]any{"error": h.badFile.Error()}}
	case h.stale:
		return Warning{Key: WarningStale, Params: map[string]any{"keys": liveNames()}}
	}
	return Warning{}
}

// Stale reports that the file changed in a way only a restart applies.
func (h *Holder) Stale() bool {
	h.refresh()
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stale
}

func (h *Holder) statFile() fileSig {
	if h.opts.Path == "" {
		return fileSig{}
	}
	fi, err := h.opts.Stat(h.opts.Path)
	if err != nil {
		return fileSig{}
	}
	return fileSig{modTime: fi.ModTime(), size: fi.Size(), exists: true}
}

// Reload reads the file now, whatever the stat interval, as a change the serve just made to it asks.
func (h *Holder) Reload() {
	var changed func()
	h.mu.Lock()
	if h.opts.Path != "" && h.opts.Load != nil {
		h.checked, h.sig = h.opts.Now(), h.statFile()
		changed = h.reload()
	}
	h.mu.Unlock()
	if changed != nil {
		changed()
	}
}

func (h *Holder) refresh() {
	var changed func()
	h.mu.Lock()
	if h.opts.Path != "" && h.opts.Load != nil && h.opts.Now().Sub(h.checked) >= h.opts.Every {
		h.checked = h.opts.Now()
		if sig := h.statFile(); sig != h.sig {
			h.sig = sig
			changed = h.reload()
		}
	}
	h.mu.Unlock()
	if changed != nil {
		changed()
	}
}

func (h *Holder) reload() func() {
	before := h.warning()
	next, err := h.opts.Load()
	h.badFile = err
	var calls []func()
	if err == nil {
		h.stale = !equalExceptLive(h.start, next)
		was := h.live.Profile
		for _, k := range liveKeys {
			k.take(&h.live, next)
		}
		if next.Profile != was {
			if on := h.opts.OnProfileChange; on != nil {
				p := next.Profile
				calls = append(calls, func() { on(p) })
			}
		}
	}
	if on := h.opts.OnWarningChange; on != nil && !h.warning().same(before) {
		calls = append(calls, on)
	}
	if len(calls) == 0 {
		return nil
	}
	return func() {
		for _, c := range calls {
			c()
		}
	}
}

func equalExceptLive(a, b Config) bool {
	for _, k := range liveKeys {
		k.take(&a, Config{})
		k.take(&b, Config{})
	}
	// The app reads window.* when it starts; no restart of the daemon applies them.
	a.Window, b.Window = Window{}, Window{}
	return reflect.DeepEqual(a, b)
}

// liveNames are the live keys as a warning lists them.
func liveNames() string {
	names := make([]string, len(liveKeys))
	for i, k := range liveKeys {
		names[i] = k.name
	}
	return strings.Join(names, ", ")
}
