package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// ---------------------------------------------------------------------------
// sanitizeSlug · pure-Go tests (no git dependency)
// ---------------------------------------------------------------------------

func TestSanitizeSlug(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"reviewer", "reviewer"},
		{"PR review / hotfix", "PR-review-hotfix"},
		{"agent:with/slash", "agent-with-slash"},
		{"---weird---", "weird"},
		{"   ", "agent"},
		{"", "agent"},
		{"UPPER_case-ok.1", "UPPER_case-ok-1"},
	}
	for _, tc := range cases {
		if got := sanitizeSlug(tc.in); got != tc.want {
			t.Errorf("sanitizeSlug(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Availability · stub when git is missing
// ---------------------------------------------------------------------------

func TestAvailable_ReflectsPathLookup(t *testing.T) {
	// We can't force the cached value off once set; just assert that it
	// matches exec.LookPath at observation time.
	_, err := exec.LookPath("git")
	want := err == nil
	if got := Available(); got != want {
		t.Fatalf("Available() = %v; want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// RunWithCwdOverride · runs regardless of git availability
// ---------------------------------------------------------------------------

func TestRunWithCwdOverride_RestoresCwd(t *testing.T) {
	tmp := t.TempDir()
	orig, _ := os.Getwd()
	var observed string
	err := RunWithCwdOverride(context.Background(), tmp, func(context.Context) error {
		cur, _ := os.Getwd()
		// On macOS, os.Getwd may return a /private-prefixed path. Tolerate.
		observed = cur
		return nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(observed), filepath.ToSlash(tmp)) && observed != tmp {
		t.Logf("observed cwd inside fn: %q (want suffix %q)", observed, tmp)
	}
	now, _ := os.Getwd()
	if now != orig {
		t.Fatalf("cwd not restored: before=%q after=%q", orig, now)
	}
}

func TestRunWithCwdOverride_EmptyCwdErrors(t *testing.T) {
	if err := RunWithCwdOverride(context.Background(), "  ", func(context.Context) error { return nil }); err == nil {
		t.Fatal("blank cwd should error")
	}
}

func TestRunWithCwdOverride_PropagatesFnError(t *testing.T) {
	tmp := t.TempDir()
	fnErr := errors.New("fn boom")
	err := RunWithCwdOverride(context.Background(), tmp, func(context.Context) error { return fnErr })
	if !errors.Is(err, fnErr) {
		t.Fatalf("got %v; want %v", err, fnErr)
	}
}

func TestRunWithCwdOverride_ConcurrencySerialized(t *testing.T) {
	tmp := t.TempDir()
	var wg sync.WaitGroup
	var active int32
	var maxActive int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = RunWithCwdOverride(context.Background(), tmp, func(context.Context) error {
				cur := atomic.AddInt32(&active, 1)
				// Track the high water mark — must stay ≤ 1 if the mutex
				// actually serialises callers.
				for {
					m := atomic.LoadInt32(&maxActive)
					if cur <= m || atomic.CompareAndSwapInt32(&maxActive, m, cur) {
						break
					}
				}
				atomic.AddInt32(&active, -1)
				return nil
			})
		}()
	}
	wg.Wait()
	if atomic.LoadInt32(&maxActive) > 1 {
		t.Fatalf("mutex did not serialise: max concurrent = %d", maxActive)
	}
}

// ---------------------------------------------------------------------------
// Git-backed integration (skip when git missing)
// ---------------------------------------------------------------------------

func requireGit(t *testing.T) string {
	t.Helper()
	if !Available() {
		t.Skip("git not on PATH")
	}
	return gitPath()
}

// initRepo creates a throwaway git repo with one commit in tmp and
// returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	runOrSkip(t, tmp, "git", "init")
	runOrSkip(t, tmp, "git", "config", "user.email", "dev@test")
	runOrSkip(t, tmp, "git", "config", "user.name", "Test Dev")
	runOrSkip(t, tmp, "git", "config", "commit.gpgsign", "false")
	// Write a file + initial commit so worktree add has something to branch from.
	if err := os.WriteFile(filepath.Join(tmp, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runOrSkip(t, tmp, "git", "add", ".")
	runOrSkip(t, tmp, "git", "commit", "-m", "seed")
	return tmp
}

func runOrSkip(t *testing.T, dir, bin string, args ...string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("setup %s %v failed: %v\n%s", bin, args, err, out)
	}
}

func TestIntegration_CreateStatusRemove(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)

	wt, err := CreateAgentWorktree(context.Background(), repo, "reviewer-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree dir missing: %v", err)
	}
	// Clean right after creation → no changes.
	dirty, err := HasWorktreeChanges(context.Background(), wt)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if dirty {
		t.Fatal("fresh worktree should not be dirty")
	}
	// Dirty it up.
	if err := os.WriteFile(filepath.Join(wt, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, _ = HasWorktreeChanges(context.Background(), wt)
	if !dirty {
		t.Fatal("after writing new file, worktree should be dirty")
	}
	// Remove the worktree.
	if err := RemoveAgentWorktree(context.Background(), wt); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Fatal("worktree dir should be gone")
	}
}

func TestIntegration_CreateRejectsEmptyRepoRoot(t *testing.T) {
	requireGit(t)
	if _, err := CreateAgentWorktree(context.Background(), "  ", "x"); err == nil {
		t.Fatal("empty repoRoot should error")
	}
}

func TestIntegration_RemoveRejectsEmptyPath(t *testing.T) {
	requireGit(t)
	if err := RemoveAgentWorktree(context.Background(), "  "); err == nil {
		t.Fatal("empty path should error")
	}
}

func TestIntegration_BranchNameIncludesSlug(t *testing.T) {
	if got := worktreeBranchName("Reviewer Bot"); !strings.Contains(got, "Reviewer-Bot") {
		t.Fatalf("branch = %q", got)
	}
}
