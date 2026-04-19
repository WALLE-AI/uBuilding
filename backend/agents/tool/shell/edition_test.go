package shell

import (
	"testing"
	"time"
)

func TestDetectPowerShellEdition_CoreFake(t *testing.T) {
	restore := SetPowerShellEditionProbe(func(time.Duration) string {
		return EditionCore
	})
	defer restore()
	if got := DetectPowerShellEdition(); got != EditionCore {
		t.Errorf("got %q, want %q", got, EditionCore)
	}
}

func TestDetectPowerShellEdition_DesktopFake(t *testing.T) {
	restore := SetPowerShellEditionProbe(func(time.Duration) string {
		return EditionDesktop
	})
	defer restore()
	if got := DetectPowerShellEdition(); got != EditionDesktop {
		t.Errorf("got %q, want %q", got, EditionDesktop)
	}
}

func TestDetectPowerShellEdition_Unknown(t *testing.T) {
	restore := SetPowerShellEditionProbe(func(time.Duration) string {
		return EditionUnknown
	})
	defer restore()
	if got := DetectPowerShellEdition(); got != EditionUnknown {
		t.Errorf("got %q, want empty", got)
	}
}

func TestDetectPowerShellEdition_CachedAcrossCalls(t *testing.T) {
	calls := 0
	restore := SetPowerShellEditionProbe(func(time.Duration) string {
		calls++
		return EditionCore
	})
	defer restore()

	for i := 0; i < 5; i++ {
		_ = DetectPowerShellEdition()
	}
	if calls != 1 {
		t.Errorf("probe invoked %d times; want 1 (cached thereafter)", calls)
	}
}
