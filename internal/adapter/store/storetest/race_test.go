//go:build race

package storetest

// race slows modernc SQLite more than twentyfold, past any wall-clock budget.
const race = true
