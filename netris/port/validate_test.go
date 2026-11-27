package port

import "testing"

func TestValidateBreakout(t *testing.T) {
	for _, v := range []string{"off", "manual", "2x200", "4x10", "4x25", "4x100"} {
		if _, errs := validateBreakout(v, "breakout"); len(errs) != 0 {
			t.Errorf("breakout %q should be accepted, got %v", v, errs)
		}
	}
	for _, v := range []string{"", "2x100", "2X200", "1x400", "8x50"} {
		if _, errs := validateBreakout(v, "breakout"); len(errs) == 0 {
			t.Errorf("breakout %q should be rejected", v)
		}
	}
}
