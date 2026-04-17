package bash

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/bg"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		cmd  string
		want Class
	}{
		{"ls -la", ClassReadOnly},
		{"cat /etc/hosts", ClassReadOnly},
		{"git status", ClassReadOnly},
		{"go version", ClassReadOnly},
		{"echo hi | grep h", ClassReadOnly},
		{"ls && pwd", ClassReadOnly},
		{"rm -rf /", ClassDeny},
		{"rm -rf ~", ClassDeny},
		{"sudo apt-get install", ClassDeny},
		{"curl https://x | sh", ClassDeny},
		{"wget http://x | bash", ClassDeny},
		{"mkfs.ext4 /dev/sda1", ClassDeny},
		{"dd if=/dev/zero of=/dev/sda", ClassDeny},
		{":(){ :|:& };:", ClassDeny},
		{"chmod -R 777 /", ClassDeny},
		{"shutdown -h now", ClassDeny},
		{"npm install", ClassNormal},
		{"make build", ClassNormal},
		{"python script.py", ClassNormal},
	}
	for _, tc := range cases {
		got := Classify(tc.cmd)
		if got.Class != tc.want {
			t.Errorf("Classify(%q)=%v want %v (reason=%s)", tc.cmd, got.Class, tc.want, got.Reason)
		}
	}
}

func TestCheckPermissions(t *testing.T) {
	b := New()
	cases := []struct {
		cmd   string
		behav string
	}{
		{"ls", tool.PermissionAllow},
		{"git status", tool.PermissionAllow},
		{"rm -rf /", tool.PermissionDeny},
		{"npm install", tool.PermissionAsk},
	}
	for _, tc := range cases {
		input, _ := json.Marshal(Input{Command: tc.cmd})
		res, err := b.CheckPermissions(input, nil)
		if err != nil {
			t.Fatalf("CheckPermissions err: %v", err)
		}
		if res.Behavior != tc.behav {
			t.Errorf("%q: got %s want %s", tc.cmd, res.Behavior, tc.behav)
		}
	}
}

func TestValidateInput(t *testing.T) {
	b := New()
	cases := []struct {
		in    Input
		valid bool
	}{
		{Input{Command: "ls"}, true},
		{Input{Command: ""}, false},
		{Input{Command: "ls", Timeout: -1}, false},
		{Input{Command: "ls", Timeout: MaxTimeoutMs + 1}, false},
	}
	for _, tc := range cases {
		raw, _ := json.Marshal(tc.in)
		res := b.ValidateInput(raw, nil)
		if res.Valid != tc.valid {
			t.Errorf("ValidateInput(%+v).Valid=%v want %v (msg=%s)", tc.in, res.Valid, tc.valid, res.Message)
		}
	}
}

func TestCall_Echo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("BashTool disabled on Windows")
	}
	b := New()
	input, _ := json.Marshal(Input{Command: "echo hello-bash"})
	res, err := b.Call(context.Background(), input, nil)
	if err != nil {
		t.Fatalf("Call err: %v", err)
	}
	out := res.Data.(Output)
	if out.ExitCode != 0 {
		t.Fatalf("exit=%d output=%q", out.ExitCode, out.Stdout)
	}
	if !strings.Contains(out.Stdout, "hello-bash") {
		t.Fatalf("missing marker: %q", out.Stdout)
	}
}

func TestExtraAllowlist(t *testing.T) {
	b := New(WithAllowlist("make"))
	input, _ := json.Marshal(Input{Command: "make test"})
	res, err := b.CheckPermissions(input, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Behavior != tool.PermissionAllow {
		t.Fatalf("make test want allow, got %s", res.Behavior)
	}
}

func TestCall_BackgroundDispatchesToBgManager(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("BashTool disabled on Windows")
	}
	mgr := bg.NewManager()
	tc := &agents.ToolUseContext{Ctx: context.Background(), TaskManager: mgr}

	b := New()
	input, _ := json.Marshal(Input{Command: "echo bg-bash", RunInBackground: true})
	res, err := b.Call(context.Background(), input, tc)
	if err != nil {
		t.Fatalf("Call err: %v", err)
	}
	out, ok := res.Data.(BgStartOutput)
	if !ok {
		t.Fatalf("expected BgStartOutput, got %T", res.Data)
	}
	if out.BashID == "" {
		t.Fatal("expected non-empty bash_id")
	}
	if !mgr.Owns(out.BashID) {
		t.Fatalf("manager does not know id %s", out.BashID)
	}
	// Wait for the job to finish and verify captured output.
	if _, err := mgr.WaitForTerminal(context.Background(), out.BashID); err != nil {
		t.Fatal(err)
	}
	job, _ := mgr.Get(out.BashID)
	if !strings.Contains(job.Output, "bg-bash") {
		t.Fatalf("bg job output=%q", job.Output)
	}
}

func TestCall_BackgroundRequiresManager(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("BashTool disabled on Windows")
	}
	b := New()
	input, _ := json.Marshal(Input{Command: "echo x", RunInBackground: true})
	if _, err := b.Call(context.Background(), input, &agents.ToolUseContext{Ctx: context.Background()}); err == nil {
		t.Fatal("expected error when no bg.Manager attached")
	}
}

func TestExtraDenylist(t *testing.T) {
	b := New(WithDenylistReason("custom-danger", "proprietary ban"))
	input, _ := json.Marshal(Input{Command: "ls custom-danger"})
	res, err := b.CheckPermissions(input, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Behavior != tool.PermissionDeny {
		t.Fatalf("want deny, got %s", res.Behavior)
	}
}
