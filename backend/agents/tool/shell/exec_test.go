package shell

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// echoSpec returns a Spec that prints the given text on any platform.
func echoSpec(text string) Spec {
	if runtime.GOOS == "windows" {
		return Spec{Cmd: "cmd", Args: []string{"/c", "echo " + text}}
	}
	return Spec{Cmd: "sh", Args: []string{"-c", "echo " + text}}
}

func TestRun_Echo(t *testing.T) {
	res, err := Run(context.Background(), echoSpec("hello-shell"))
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d output=%q", res.ExitCode, res.Output)
	}
	if !strings.Contains(res.Output, "hello-shell") {
		t.Fatalf("output missing marker: %q", res.Output)
	}
	if res.TimedOut || res.Canceled || res.Truncated {
		t.Fatalf("unexpected flags: %+v", res)
	}
}

func TestRun_NonZeroExit(t *testing.T) {
	var spec Spec
	if runtime.GOOS == "windows" {
		spec = Spec{Cmd: "cmd", Args: []string{"/c", "exit 7"}}
	} else {
		spec = Spec{Cmd: "sh", Args: []string{"-c", "exit 7"}}
	}
	res, err := Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("want exit=7 got %d", res.ExitCode)
	}
}

func TestRun_Timeout(t *testing.T) {
	var spec Spec
	if runtime.GOOS == "windows" {
		spec = Spec{Cmd: "powershell", Args: []string{"-NoProfile", "-Command", "Start-Sleep -Seconds 5"}}
	} else {
		spec = Spec{Cmd: "sh", Args: []string{"-c", "sleep 5"}}
	}
	spec.TimeoutMs = 200
	start := time.Now()
	res, err := Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("expected TimedOut=true, got %+v", res)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("timeout kill too slow: %v", time.Since(start))
	}
}

func TestRun_ContextCancel(t *testing.T) {
	var spec Spec
	if runtime.GOOS == "windows" {
		spec = Spec{Cmd: "powershell", Args: []string{"-NoProfile", "-Command", "Start-Sleep -Seconds 5"}}
	} else {
		spec = Spec{Cmd: "sh", Args: []string{"-c", "sleep 5"}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)
	res, err := Run(ctx, spec)
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if !res.Canceled && !res.TimedOut {
		t.Fatalf("expected Canceled=true, got %+v", res)
	}
}

func TestRun_TruncateOutput(t *testing.T) {
	var spec Spec
	// Emit ~10KB by repeating a small string.
	if runtime.GOOS == "windows" {
		spec = Spec{Cmd: "powershell", Args: []string{"-NoProfile", "-Command", "1..1000 | ForEach-Object { 'AAAAAAAAAA' }"}}
	} else {
		spec = Spec{Cmd: "sh", Args: []string{"-c", "yes AAAAAAAAAA | head -n 1000"}}
	}
	spec.MaxOutputBytes = 512
	res, err := Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if !res.Truncated {
		t.Fatalf("expected Truncated=true, got %+v len=%d", res, len(res.Output))
	}
	if len(res.Output) > 512 {
		t.Fatalf("output exceeds cap: %d", len(res.Output))
	}
}

func TestMergeEnv_Override(t *testing.T) {
	merged := mergeEnv([]string{"PATH=/custom"})
	var found string
	for _, kv := range merged {
		if strings.HasPrefix(kv, "PATH=") {
			found = kv
		}
	}
	if found != "PATH=/custom" {
		t.Fatalf("want PATH=/custom, got %q", found)
	}
}
