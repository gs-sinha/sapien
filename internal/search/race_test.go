//go:build race

package search_test

// raceEnabled relaxes the timing budget when the race detector is on: the
// detector alone is 5-10x slower, and the whole-repo race run schedules
// every package at once.
const raceEnabled = true
