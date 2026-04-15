package agents_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func TestProcessAtMentionedFiles_NoMentions(t *testing.T) {
	results := agents.ProcessAtMentionedFiles("hello world", ".", 0)
	assert.Empty(t, results)
}

func TestProcessAtMentionedFiles_WithFile(t *testing.T) {
	dir := t.TempDir()
	fpath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(fpath, []byte("hello content"), 0644))

	input := "check @" + fpath + " please"
	results := agents.ProcessAtMentionedFiles(input, dir, 0)
	require.Len(t, results, 1)
	assert.Equal(t, "hello content", results[0].Content)
	assert.False(t, results[0].Truncated)
}

func TestProcessAtMentionedFiles_RelativePath(t *testing.T) {
	dir := t.TempDir()
	fpath := filepath.Join(dir, "sub", "file.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(fpath), 0755))
	require.NoError(t, os.WriteFile(fpath, []byte("package main"), 0644))

	input := "look at @sub/file.go"
	results := agents.ProcessAtMentionedFiles(input, dir, 0)
	require.Len(t, results, 1)
	assert.Contains(t, results[0].Content, "package main")
	assert.Contains(t, results[0].DisplayPath, "sub")
}

func TestProcessAtMentionedFiles_Truncation(t *testing.T) {
	dir := t.TempDir()
	fpath := filepath.Join(dir, "big.txt")
	require.NoError(t, os.WriteFile(fpath, []byte("abcdefghij"), 0644))

	input := "@" + fpath
	results := agents.ProcessAtMentionedFiles(input, dir, 5) // limit to 5 bytes
	require.Len(t, results, 1)
	assert.Equal(t, "abcde", results[0].Content)
	assert.True(t, results[0].Truncated)
}

func TestProcessAtMentionedFiles_DeduplicatesSamePath(t *testing.T) {
	dir := t.TempDir()
	fpath := filepath.Join(dir, "dup.txt")
	require.NoError(t, os.WriteFile(fpath, []byte("content"), 0644))

	input := "@" + fpath + " @" + fpath
	results := agents.ProcessAtMentionedFiles(input, dir, 0)
	assert.Len(t, results, 1)
}

func TestProcessAtMentionedFiles_SkipsNonExistent(t *testing.T) {
	results := agents.ProcessAtMentionedFiles("@/nonexistent/file.txt", ".", 0)
	assert.Empty(t, results)
}

func TestProcessAtMentionedFiles_SkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	input := "@" + dir
	results := agents.ProcessAtMentionedFiles(input, dir, 0)
	assert.Empty(t, results)
}

func TestCreateFileAttachmentMessage(t *testing.T) {
	msg := agents.CreateFileAttachmentMessage(agents.FileAttachmentResult{
		Filename: "/tmp/test.txt",
		Content:  "content",
	})
	assert.Equal(t, agents.MessageTypeAttachment, msg.Type)
	assert.Equal(t, "file", msg.Attachment.Type)
}

func TestCreateMaxTurnsAttachment(t *testing.T) {
	msg := agents.CreateMaxTurnsAttachment(10, 10)
	assert.Equal(t, agents.MessageTypeAttachment, msg.Type)
	assert.Equal(t, "max_turns_reached", msg.Attachment.Type)
	assert.Equal(t, 10, msg.Attachment.MaxTurns)
}

func TestCreateCriticalReminderAttachment(t *testing.T) {
	msg := agents.CreateCriticalReminderAttachment("important!")
	assert.Equal(t, agents.MessageTypeAttachment, msg.Type)
	assert.Equal(t, "critical_system_reminder", msg.Attachment.Type)
}

func TestAttachmentCollector_Collect(t *testing.T) {
	collector := agents.NewAttachmentCollector()

	collector.Register("test1", func(ctx context.Context, opts agents.AttachmentOptions) []agents.Message {
		return []agents.Message{{Type: agents.MessageTypeAttachment, Attachment: &agents.AttachmentData{Type: "test1"}}}
	})
	collector.Register("test2", func(ctx context.Context, opts agents.AttachmentOptions) []agents.Message {
		return nil // no attachment
	})
	collector.Register("test3", func(ctx context.Context, opts agents.AttachmentOptions) []agents.Message {
		return []agents.Message{{Type: agents.MessageTypeAttachment, Attachment: &agents.AttachmentData{Type: "test3"}}}
	})

	msgs := collector.Collect(context.Background(), agents.AttachmentOptions{})
	assert.Len(t, msgs, 2)
}

func TestAttachmentCollector_CancelledContext(t *testing.T) {
	collector := agents.NewAttachmentCollector()
	called := false
	collector.Register("should_not_run", func(ctx context.Context, opts agents.AttachmentOptions) []agents.Message {
		called = true
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	collector.Collect(ctx, agents.AttachmentOptions{})
	assert.False(t, called)
}

func TestAtMentionedFilesProvider_NoAtSign(t *testing.T) {
	msgs := agents.AtMentionedFilesProvider(context.Background(), agents.AttachmentOptions{
		Input: "no mentions here",
		Cwd:   ".",
	})
	assert.Nil(t, msgs)
}

func TestAtMentionedFilesProvider_WithFile(t *testing.T) {
	dir := t.TempDir()
	fpath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(fpath, []byte("data"), 0644))

	msgs := agents.AtMentionedFilesProvider(context.Background(), agents.AttachmentOptions{
		Input: "@" + fpath,
		Cwd:   dir,
	})
	require.Len(t, msgs, 1)
	assert.Equal(t, agents.MessageTypeAttachment, msgs[0].Type)
}
