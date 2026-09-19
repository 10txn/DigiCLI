package agent

import (
	"fmt"

	"github.com/10txn/digicli/internal/types"
)

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

// Permit decides whether a call may run in a mode.
//
// There are three questions, in this order, because they are not equally
// serious:
//
//  1. Is the path one DigiCLI refuses outright? Credentials, the operating
//     system's own files, anything that runs commands later. No mode relaxes
//     this and neither does approval — see file.Guard for why.
//  2. Does the call leave the working directory? Refused in plan, put to the
//     user in manual, allowed in auto. This is asked about reads as well as
//     writes: a read outside the tree is how a private file ends up in a
//     remote provider's request logs.
//  3. Does the call change anything? Refused in plan, put to the user in
//     manual, allowed in auto.
//
// Reading inside the working directory is allowed everywhere: plan mode exists
// to stop changes, not to stop the model looking at code.
func Permit(mode types.Mode, reach types.Reach) Decision {
	if reach.Refusal != "" {
		return Deny
	}
	if reach.Outside {
		return gate(mode)
	}
	if !reach.Mutates {
		return Allow
	}
	return gate(mode)
}

// gate is what plan, manual and auto each do with a call that is not allowed
// outright: refuse it, ask about it, or run it.
func gate(mode types.Mode) Decision {
	switch mode {
	case types.ModePlan:
		return Deny
	case types.ModeManual:
		return Ask
	default:
		return Allow
	}
}

// DenialReason is what the model is told when a call is refused, phrased so it
// changes approach rather than retrying the same call. Which of the three
// rules refused it matters to the model: two of them it can work around by
// staying inside the working directory, and the first it cannot work around at
// all, which is worth saying plainly so it stops trying.
func DenialReason(mode types.Mode, tool string, reach types.Reach) string {
	if reach.Refusal != "" {
		return "Refused: " + reach.Refusal + ". This does not depend on the mode and " +
			"the user cannot approve it. Do not try again, and do not look for another " +
			"path to the same place — say what you were trying to do and why instead."
	}
	if reach.Outside {
		return fmt.Sprintf("Refused: that path is outside the working directory, and the "+
			"session is in %s mode. Do not try again. Work within the working directory, "+
			"or tell the user what you need from outside it and let them decide.", mode)
	}
	return "Refused: " + tool + " would make changes, and the session is in " +
		mode.String() + " mode, which is read-only. Do not try again. " +
		"Describe the change you would make instead, and let the user decide."
}

// RefusalReason is what the model is told when the user was asked and said no.
// It is not a mistake to correct, so the model is pointed at asking rather than
// at trying something else.
func RefusalReason(tool string) string {
	return "The user was asked to approve this " + tool + " call and declined. " +
		"Do not run it again or work around it. Ask what they would prefer."
}
