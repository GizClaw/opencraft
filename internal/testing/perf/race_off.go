//go:build !race

package perf

func init() { raceFlag = false }
