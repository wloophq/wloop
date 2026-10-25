//go:build race

package prrow_test

// race slows modernc SQLite past any wall-clock budget.
const race = true
