package agent

import (
	"strings"
	"testing"

	"github.com/10txn/digicli/internal/types"
)

var modes = []types.Mode{types.ModePlan, types.ModeManual, types.ModeAuto}

// The whole policy in one table, because the interesting part is the shape of
// it rather than any single cell: what a mode does depends on where the call
// reaches, not only on whether it writes.
func TestPermit(t *testing.T) {
	tests := []struct {
		name  string
		reach types.Reach
		want  map[types.Mode]Decision
	}{
		{
			name:  "reading inside the working directory",
			reach: types.Reach{Path: "/work/main.go"},
			want: map[types.Mode]Decision{
				types.ModePlan: Allow, types.ModeManual: Allow, types.ModeAuto: Allow,
			},
		},
		{
			name:  "writing inside the working directory",
			reach: types.Reach{Mutates: true, Path: "/work/main.go"},
			want: map[types.Mode]Decision{
				types.ModePlan: Deny, types.ModeManual: Ask, types.ModeAuto: Allow,
			},
		},
		{
			name:  "reading outside the working directory",
			reach: types.Reach{Path: "/elsewhere/notes.md", Outside: true},
			want: map[types.Mode]Decision{
				types.ModePlan: Deny, types.ModeManual: Ask, types.ModeAuto: Allow,
			},
		},
		{
			name:  "writing outside the working directory",
			reach: types.Reach{Mutates: true, Path: "/elsewhere/notes.md", Outside: true},
			want: map[types.Mode]Decision{
				types.ModePlan: Deny, types.ModeManual: Ask, types.ModeAuto: Allow,
			},
		},
		{
			name:  "a path the guard refuses",
			reach: types.Reach{Mutates: true, Path: "/home/me/.ssh/id_rsa", Refusal: "credentials"},
			want: map[types.Mode]Decision{
				types.ModePlan: Deny, types.ModeManual: Deny, types.ModeAuto: Deny,
			},
		},
	}

	for _, tt := range tests {
		for _, mode := range modes {
			got := Permit(mode, tt.reach)
			if got != tt.want[mode] {
				t.Errorf("%s in %s mode: Permit = %v, want %v",
					tt.name, mode, got, tt.want[mode])
			}
		}
	}
}

// The guard is the one rule with no way round it, so auto mode is the case
// worth stating on its own: it is the mode that reaches outside the tree
// unattended, and the one where nobody is watching.
func TestGuardRefusalSurvivesAutoMode(t *testing.T) {
	reach := types.Reach{Mutates: true, Outside: true, Refusal: "credentials"}

	if got := Permit(types.ModeAuto, reach); got != Deny {
		t.Errorf("Permit in auto mode = %v, want Deny", got)
	}
}

// A refusal the model can work around and one it cannot are different
// messages, because it will keep trying if it is not told which it has hit.
func TestDenialReasonDistinguishesTheRules(t *testing.T) {
	guarded := DenialReason(types.ModeAuto, "write_file",
		types.Reach{Mutates: true, Refusal: "~/.ssh holds credentials"})
	if !strings.Contains(guarded, "~/.ssh") {
		t.Errorf("a guard refusal does not say what it refused: %q", guarded)
	}
	if !strings.Contains(guarded, "cannot approve") {
		t.Errorf("a guard refusal does not say the user cannot lift it: %q", guarded)
	}

	outside := DenialReason(types.ModePlan, "read_file", types.Reach{Outside: true})
	if !strings.Contains(outside, "outside the working directory") {
		t.Errorf("an out-of-tree refusal does not say so: %q", outside)
	}

	readOnly := DenialReason(types.ModePlan, "write_file", types.Reach{Mutates: true})
	if !strings.Contains(readOnly, "read-only") {
		t.Errorf("a plan-mode refusal does not say so: %q", readOnly)
	}
}
