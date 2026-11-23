package driver

import "time"

// StartupGate is a command in the main container that must succeed before any
// further container starts. The driver bounds it by Timeout and cancellation.
type StartupGate struct {
	Arguments []string
	Timeout   time.Duration
}
