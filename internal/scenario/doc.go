// Package scenario defines the Scenario interface, the built-in catalog, parsing
// of the X-Sandbox-Scenario header and matching of rules from the rules file.
//
// A scenario has two hooks: one decides the synchronous answer to a create request
// and the schedule of status changes, the other decides how each event is delivered.
package scenario
