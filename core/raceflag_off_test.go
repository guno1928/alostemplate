//go:build !race

package core

// raceEnabled reports whether the binary was built with the race detector.
const raceEnabled = false
