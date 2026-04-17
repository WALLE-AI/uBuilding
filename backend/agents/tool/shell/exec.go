// Package shell provides a cross-platform command executor used by the Bash
// and PowerShell tools. It wraps os/exec with timeout, cancellation, merged
// output capture, and size-truncation semantics comparable to
// claude-code-main's BashTool execution path.
package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Spec describes a command to be executed.
type Spec struct {
	// Cmd is the program to invoke (e.g., "bash", "sh", "powershell").
	Cmd string
	// Args are the program arguments.
	Args []string
	// Cwd is the working directory. Empty means inherit current process cwd.
	Cwd string
	// Env are additional "KEY=VALUE" entries merged with the parent environment.
	Env []string
	// Stdin, if non-empty, is piped to the command's stdin.
	Stdin string
	// TimeoutMs caps the command duration. 0 means no explicit timeout (the
	// caller's context still applies).
	TimeoutMs int
	// MaxOutputBytes truncates merged stdout/stderr capture. 0 means default
	// (1 MiB). Negative means unlimited.
	MaxOutputBytes int
}

// Result is the outcome of a command execution.
type Result struct {
	// ExitCode is the process exit code. -1 indicates the process was killed
	// (timeout, cancel, or signal) before producing an exit status.
	ExitCode int
	// Output is the merged (stdout+stderr) output, possibly truncated.
	Output string
	// Truncated is true when Output was cut at MaxOutputBytes.
	Truncated bool
	// TimedOut indicates the command was killed because TimeoutMs elapsed.
	TimedOut bool
	// Canceled indicates the parent context was cancelled before completion.
	Canceled bool
	// Duration records how long the process ran.
	Duration time.Duration
}

// DefaultMaxOutputBytes is the default cap for merged output capture.
const DefaultMaxOutputBytes = 1 << 20 // 1 MiB

// Run executes spec honoring ctx cancellation and spec.TimeoutMs.
func Run(ctx context.Context, spec Spec) (*Result, error) {
	if spec.Cmd == "" {
		return nil, errors.New("shell.Run: empty Cmd")
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if spec.TimeoutMs > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(spec.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, spec.Cmd, spec.Args...)
	cmd.Cancel = func() error { return killProcess(cmd) }
	if spec.Cwd != "" {
		cmd.Dir = spec.Cwd
	}
	if len(spec.Env) > 0 {
		// Inherit parent env; append caller-supplied entries so they take
		// precedence for duplicate keys (exec uses last-wins on some platforms
		// but to be explicit we de-dupe ourselves).
		cmd.Env = mergeEnv(spec.Env)
	}

	max := spec.MaxOutputBytes
	if max == 0 {
		max = DefaultMaxOutputBytes
	}
	buf := &truncatingBuffer{max: max}
	cmd.Stdout = buf
	cmd.Stderr = buf
	if spec.Stdin != "" {
		cmd.Stdin = strings.NewReader(spec.Stdin)
	}

	setProcAttrs(cmd)

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("shell.Run: start %q: %w", spec.Cmd, err)
	}
	waitErr := cmd.Wait()
	dur := time.Since(start)

	res := &Result{
		Output:    buf.String(),
		Truncated: buf.truncated,
		Duration:  dur,
	}

	// Classify termination cause.
	switch {
	case runCtx.Err() == context.DeadlineExceeded:
		res.TimedOut = true
	case ctx.Err() == context.Canceled:
		res.Canceled = true
	}

	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			res.ExitCode = ee.ExitCode()
		} else {
			res.ExitCode = -1
		}
	} else {
		res.ExitCode = 0
	}
	return res, nil
}

// truncatingBuffer is an io.Writer that stops appending once max bytes have
// been captured but continues to accept writes (to avoid blocking the child).
type truncatingBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *truncatingBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max < 0 {
		return b.buf.Write(p)
	}
	remaining := b.max - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *truncatingBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// mergeEnv combines os.Environ() with the caller-provided entries, letting
// caller entries win on duplicate keys.
func mergeEnv(extra []string) []string {
	base := os.Environ()
	idx := make(map[string]int, len(base))
	for i, kv := range base {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			idx[kv[:eq]] = i
		}
	}
	out := make([]string, len(base))
	copy(out, base)
	for _, kv := range extra {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			out = append(out, kv)
			continue
		}
		key := kv[:eq]
		if i, ok := idx[key]; ok {
			out[i] = kv
		} else {
			idx[key] = len(out)
			out = append(out, kv)
		}
	}
	return out
}
