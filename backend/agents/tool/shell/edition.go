package shell

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// PowerShell edition detection
//
// Windows ships two families of PowerShell:
//
//   - "desktop" → Windows PowerShell 5.1 (bundled with the OS, `powershell.exe`)
//   - "core"    → PowerShell 7+ (cross-platform, `pwsh` / `pwsh.exe`)
//
// The two speak (mostly) the same dialect but the 7+ line adds:
//
//   - pipeline chain operators (`&&`, `||`)
//   - ternary / null-coalescing / null-conditional operators
//   - UTF-8 default encoding (no BOM)
//
// Our Prompt() text for the PowerShell tool needs to warn the model about
// the 5.1-specific limitations so we expose a one-shot detector. The
// detection runs at most once per process (the result is cached) and only
// fires on Windows — on *nix we assume `pwsh` == core when installed and
// "" otherwise, because there is no desktop edition off Windows.
//
// The detector has a tight 2-second timeout. If PowerShell is not on PATH
// or the probe times out we return "" — callers must fall back to the
// conservative 5.1-safe prompt guidance.
// ---------------------------------------------------------------------------

// Known return values for DetectPowerShellEdition.
const (
	EditionDesktop = "desktop" // Windows PowerShell 5.1
	EditionCore    = "core"    // PowerShell 7+
	EditionUnknown = ""        // absent / probe failed
)

var (
	editionOnce   sync.Once
	editionCached string
)

// DetectPowerShellEdition probes the local PowerShell install and returns
// one of EditionDesktop / EditionCore / EditionUnknown. The probe runs
// once per process; subsequent calls return the cached value.
func DetectPowerShellEdition() string {
	editionOnce.Do(func() {
		editionCached = probeEdition(2 * time.Second)
	})
	return editionCached
}

// ResetPowerShellEditionCache clears the cached probe result. Tests call
// this to re-probe under different fakes; production callers should not.
func ResetPowerShellEditionCache() {
	editionOnce = sync.Once{}
	editionCached = ""
}

// probeEdition runs the detection synchronously with the supplied timeout.
// Exposed for tests via SetPowerShellEditionProbe.
var probeEdition = defaultProbeEdition

func defaultProbeEdition(timeout time.Duration) string {
	// Off Windows, only `pwsh` matters. When present it is core.
	if runtime.GOOS != "windows" {
		if _, err := exec.LookPath("pwsh"); err == nil {
			return EditionCore
		}
		return EditionUnknown
	}

	// On Windows prefer pwsh (7+); fall back to powershell (5.1).
	if edition := probeBinary("pwsh", timeout); edition != EditionUnknown {
		return edition
	}
	return probeBinary("powershell", timeout)
}

func probeBinary(binName string, timeout time.Duration) string {
	if _, err := exec.LookPath(binName); err != nil {
		return EditionUnknown
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// -NoProfile avoids user profile side-effects; -Command runs the
	// probe and emits the edition string on stdout.
	cmd := exec.CommandContext(ctx, binName,
		"-NoProfile", "-NonInteractive",
		"-Command", "$PSVersionTable.PSEdition")
	out, err := cmd.Output()
	if err != nil {
		return EditionUnknown
	}
	switch strings.TrimSpace(strings.ToLower(string(out))) {
	case "desktop":
		return EditionDesktop
	case "core":
		return EditionCore
	default:
		return EditionUnknown
	}
}

// SetPowerShellEditionProbe replaces the probe function for tests.
// The returned restore function reverts to the default probe.
func SetPowerShellEditionProbe(fn func(time.Duration) string) (restore func()) {
	prev := probeEdition
	probeEdition = fn
	ResetPowerShellEditionCache()
	return func() {
		probeEdition = prev
		ResetPowerShellEditionCache()
	}
}
