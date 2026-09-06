//go:build race

package catalog_test

// raceEnabled relaxes timing thresholds when the race detector is on (5-10x slower).
const raceEnabled = true
