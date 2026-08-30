package app

import "time"

type Config struct {
	WebEnvPath string
	WebDataDir string
	ExploPath  string
	// RunTimeout kills a run that has outlasted it. Zero means no timeout.
	RunTimeout time.Duration
}
