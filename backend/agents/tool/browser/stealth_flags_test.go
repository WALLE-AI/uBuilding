package browser

import (
	"testing"
)

func TestStealthFlagsCount(t *testing.T) {
	// Scrapling has 11 DEFAULT_ARGS + ~55 STEALTH_ARGS = 66+ total flags
	if got := len(stealthLauncherFlags); got < 60 {
		t.Errorf("stealthLauncherFlags has %d entries, want >= 60", got)
	}
}

func TestHarmfulFlagsComplete(t *testing.T) {
	// Must contain all 5 flags from Scrapling HARMFUL_ARGS
	expected := []string{
		"enable-automation",
		"disable-popup-blocking",
		"disable-component-update",
		"disable-default-apps",
		"disable-extensions",
	}
	if len(harmfulFlags) != len(expected) {
		t.Fatalf("harmfulFlags has %d entries, want %d", len(harmfulFlags), len(expected))
	}
	set := make(map[string]bool, len(harmfulFlags))
	for _, f := range harmfulFlags {
		set[f] = true
	}
	for _, e := range expected {
		if !set[e] {
			t.Errorf("harmfulFlags missing %q", e)
		}
	}
}

func TestConditionalStealthFlags(t *testing.T) {
	tests := []struct {
		name         string
		webrtc       bool
		canvas       bool
		webgl        bool
		expectKeys   []string
		expectAbsent []string
	}{
		{
			name:       "all false returns empty",
			expectKeys: nil,
		},
		{
			name:       "webrtc only",
			webrtc:     true,
			expectKeys: []string{"webrtc-ip-handling-policy", "force-webrtc-ip-handling-policy"},
		},
		{
			name:       "canvas only",
			canvas:     true,
			expectKeys: []string{"fingerprinting-canvas-image-data-noise"},
		},
		{
			name:       "webgl only",
			webgl:      true,
			expectKeys: []string{"disable-webgl", "disable-webgl-image-chromium", "disable-webgl2"},
		},
		{
			name:       "all true",
			webrtc:     true,
			canvas:     true,
			webgl:      true,
			expectKeys: []string{"webrtc-ip-handling-policy", "force-webrtc-ip-handling-policy", "fingerprinting-canvas-image-data-noise", "disable-webgl", "disable-webgl-image-chromium", "disable-webgl2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := ConditionalStealthFlags(tt.webrtc, tt.canvas, tt.webgl)
			for _, k := range tt.expectKeys {
				if _, ok := flags[k]; !ok {
					t.Errorf("expected key %q not found", k)
				}
			}
			if tt.expectKeys == nil && len(flags) != 0 {
				t.Errorf("expected empty map, got %d entries", len(flags))
			}
		})
	}
}
