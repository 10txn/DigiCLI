package agent

import "github.com/10txn/digicli/internal/types"

// Decision is what a mode says should happen to a tool call.
type Decision int

const (
	// Allow runs the tool immediately.
	Allow Decision = iota
	// Ask puts the call to the user first.
	Ask
	// Deny refuses it and tells the model why.
	Deny
)

// Permit decides whether a tool may run in a mode.
//
// Reading is allowed everywhere — plan mode exists to stop changes, not to
// stop the model looking at code. Anything that mutates is denied in plan,
// queued for approval in manual, and run unattended in auto.
func Permit(mode types.Mode, mutates bool) Decision {
	if !mutates {
		return Allow
	}
	switch mode {
	case types.ModePlan:
		return Deny
	case types.ModeManual:
		return Ask
	default:
		return Allow
	}
}

// DenialReason is what the model is told when a mode refuses a call, phrased
// so it changes approach rather than retrying the same call.
func DenialReason(mode types.Mode, tool string) string {
	return "Refused: " + tool + " would make changes, and the session is in " +
		mode.String() + " mode, which is read-only. Do not try again. " +
		"Describe the change you would make instead, and let the user decide."
}
