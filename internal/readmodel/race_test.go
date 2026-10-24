//go:build race

package readmodel_test

// race slows modernc SQLite past any wall-clock budget.
const race = true
