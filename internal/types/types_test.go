package types

import (
	"encoding/json"
	"testing"
)

func TestModeRoundTripsThroughJSON(t *testing.T) {
	for _, mode := range []Mode{ModePlan, ModeManual, ModeAuto} {
		data, err := json.Marshal(mode)
		if err != nil {
			t.Fatalf("marshal %v: %v", mode, err)
		}

		var got Mode
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", data, err)
		}
		if got != mode {
			t.Errorf("round trip: got %v, want %v", got, mode)
		}
	}
}

func TestModeMarshalsAsString(t *testing.T) {
	data, err := json.Marshal(ModeManual)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `"manual"` {
		t.Errorf("got %s, want \"manual\"", data)
	}
}

func TestUnmarshalRejectsUnknownMode(t *testing.T) {
	var mode Mode
	if err := json.Unmarshal([]byte(`"turbo"`), &mode); err == nil {
		t.Fatal("expected an error for an unknown mode")
	}
}

func TestModeNextCycles(t *testing.T) {
	got := ModePlan
	want := []Mode{ModeManual, ModeAuto, ModePlan}
	for _, expected := range want {
		got = got.Next()
		if got != expected {
			t.Fatalf("Next(): got %v, want %v", got, expected)
		}
	}
}

func TestParseMode(t *testing.T) {
	if mode, err := ParseMode("auto"); err != nil || mode != ModeAuto {
		t.Errorf(`ParseMode("auto") = %v, %v`, mode, err)
	}
	if _, err := ParseMode("nope"); err == nil {
		t.Error("expected an error for an unknown mode")
	}
}
