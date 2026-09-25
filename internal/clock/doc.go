// Package clock abstracts time so delayed scenarios can be driven by tests.
// Real wraps time.Now and timers; Manual only moves forward on Advance.
package clock
