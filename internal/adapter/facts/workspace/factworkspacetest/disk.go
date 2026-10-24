// Package factworkspacetest holds fakes for the workspace facts provider.
package factworkspacetest

import (
	"context"
	"sync"
	"time"

	factworkspace "github.com/wloophq/wloop/internal/adapter/facts/workspace"
)

var _ factworkspace.Disk = (*Disk)(nil)

// Disk is an in-memory factworkspace.Disk: a fixture sets the times that
// drive workspace.last_activity and quiet_for without touching files.
type Disk struct {
	mu          sync.Mutex
	exists      map[string]bool
	touched     map[string]time.Time
	index       map[string]time.Time
	size        map[string]int64
	sizes       int
	hadDeadline bool
}

func NewDisk() *Disk {
	return &Disk{exists: map[string]bool{}, touched: map[string]time.Time{}, index: map[string]time.Time{}, size: map[string]int64{}}
}

// Put makes path exist, its HEAD last moved at touched.
func (d *Disk) Put(path string, touched time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.exists[path] = true
	d.touched[path] = touched
}

// Remove makes path absent.
func (d *Disk) Remove(path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.exists, path)
	delete(d.touched, path)
}

func (d *Disk) SetTouched(path string, at time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.touched[path] = at
}

func (d *Disk) SetIndex(path string, at time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.index[path] = at
}

func (d *Disk) SetSize(path string, n int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.size[path] = n
}

func (d *Disk) Exists(path string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.exists[path]
}

func (d *Disk) Touched(path string) time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.touched[path]
}

func (d *Disk) Index(path string) time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.index[path]
}

func (d *Disk) Size(ctx context.Context, path string) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sizes++
	_, d.hadDeadline = ctx.Deadline()
	return d.size[path], nil
}

// Sized counts the Size calls; Deadline tells whether the last had one.
func (d *Disk) Sized() (calls int, deadline bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sizes, d.hadDeadline
}
