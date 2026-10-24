package config

import (
	"os"
	"reflect"
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

// Holder is the one configuration a serve holds. Config is the config the
// serve started with, except that the profile setting follows the file; every
// other change makes Stale true until the serve restarts.
type Holder struct {
	opts HolderOptions

	mu      sync.Mutex
	start   Config
	profile string
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
	h.start, h.profile, h.stale, h.broken, h.badFile = cfg, cfg.Profile, false, nil, nil
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

// Config is the config the serve runs with and the profile now in force.
func (h *Holder) Config() Config {
	h.refresh()
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.start
	c.Profile = h.profile
	return c
}

// Warning is what a restart or a fix of config.json would change: the load
// failure of a file that broke, else that a changed file needs a restart; ""
// when the running config matches the file.
func (h *Holder) Warning() string {
	h.refresh()
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.warning()
}

func (h *Holder) warning() string {
	switch {
	case h.badFile != nil:
		return "config.json does not load, so the running config stays: " + h.badFile.Error()
	case h.stale:
		return "config.json changed; only the profile applies live, restart the daemon to apply the rest"
	}
	return ""
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
		h.stale = !equalExceptProfile(h.start, next)
		if next.Profile != h.profile {
			h.profile = next.Profile
			if on := h.opts.OnProfileChange; on != nil {
				p := next.Profile
				calls = append(calls, func() { on(p) })
			}
		}
	}
	if on := h.opts.OnWarningChange; on != nil && h.warning() != before {
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

func equalExceptProfile(a, b Config) bool {
	a.Profile, b.Profile = "", ""
	return reflect.DeepEqual(a, b)
}
