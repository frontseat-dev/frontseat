package program

import "time"

// Hurried makes a program told to end, and what it printed, waited for
// only so long, for a test, and returns what puts it back.
func Hurried(told, read time.Duration) func() {
	was, wasRead := grace, drain
	grace, drain = told, read
	return func() { grace, drain = was, wasRead }
}
