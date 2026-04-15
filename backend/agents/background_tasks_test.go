package agents_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func TestBackgroundTaskManager_RegisterAndStart(t *testing.T) {
	mgr := agents.NewBackgroundTaskManager(nil)
	defer mgr.Shutdown(time.Second)

	var ran atomic.Bool
	mgr.RegisterFactory(agents.TaskTypePromptSuggestion, func(ctx context.Context, taskCtx agents.BackgroundTaskContext) (interface{}, error) {
		ran.Store(true)
		return "suggestion result", nil
	})

	taskID := mgr.Start(agents.TaskTypePromptSuggestion, agents.BackgroundTaskContext{
		SessionID: "s1",
	})
	require.NotEmpty(t, taskID)

	mgr.Wait()
	assert.True(t, ran.Load())

	task := mgr.GetTask(taskID)
	require.NotNil(t, task)
	assert.Equal(t, agents.TaskStatusCompleted, task.Status)
	assert.Equal(t, "suggestion result", task.Result)
	assert.NoError(t, task.Error)
}

func TestBackgroundTaskManager_UnregisteredType(t *testing.T) {
	mgr := agents.NewBackgroundTaskManager(nil)
	defer mgr.Shutdown(time.Second)

	taskID := mgr.Start(agents.TaskTypeAutoDream, agents.BackgroundTaskContext{})
	assert.Empty(t, taskID)
}

func TestBackgroundTaskManager_FailedTask(t *testing.T) {
	mgr := agents.NewBackgroundTaskManager(nil)
	defer mgr.Shutdown(time.Second)

	mgr.RegisterFactory(agents.TaskTypeMemoryExtraction, func(ctx context.Context, taskCtx agents.BackgroundTaskContext) (interface{}, error) {
		return nil, errors.New("extraction failed")
	})

	taskID := mgr.Start(agents.TaskTypeMemoryExtraction, agents.BackgroundTaskContext{})
	mgr.Wait()

	task := mgr.GetTask(taskID)
	require.NotNil(t, task)
	assert.Equal(t, agents.TaskStatusFailed, task.Status)
	assert.Error(t, task.Error)
}

func TestBackgroundTaskManager_CancelAll(t *testing.T) {
	mgr := agents.NewBackgroundTaskManager(nil)

	mgr.RegisterFactory(agents.TaskTypePromptSuggestion, func(ctx context.Context, taskCtx agents.BackgroundTaskContext) (interface{}, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	taskID := mgr.Start(agents.TaskTypePromptSuggestion, agents.BackgroundTaskContext{})
	// Give task a moment to start
	time.Sleep(10 * time.Millisecond)

	mgr.CancelAll()
	mgr.Wait()

	task := mgr.GetTask(taskID)
	require.NotNil(t, task)
	assert.Equal(t, agents.TaskStatusCancelled, task.Status)
}

func TestBackgroundTaskManager_ActiveCount(t *testing.T) {
	mgr := agents.NewBackgroundTaskManager(nil)
	defer mgr.Shutdown(time.Second)

	started := make(chan struct{})
	release := make(chan struct{})

	mgr.RegisterFactory(agents.TaskTypePromptSuggestion, func(ctx context.Context, taskCtx agents.BackgroundTaskContext) (interface{}, error) {
		close(started)
		<-release
		return nil, nil
	})

	mgr.Start(agents.TaskTypePromptSuggestion, agents.BackgroundTaskContext{})
	<-started

	assert.Equal(t, 1, mgr.ActiveCount())

	close(release)
	mgr.Wait()
	assert.Equal(t, 0, mgr.ActiveCount())
}

func TestBackgroundTaskManager_WaitWithTimeout(t *testing.T) {
	mgr := agents.NewBackgroundTaskManager(nil)

	mgr.RegisterFactory(agents.TaskTypeAutoDream, func(ctx context.Context, taskCtx agents.BackgroundTaskContext) (interface{}, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	mgr.Start(agents.TaskTypeAutoDream, agents.BackgroundTaskContext{})

	// Should timeout since the task blocks forever
	ok := mgr.WaitWithTimeout(50 * time.Millisecond)
	assert.False(t, ok)

	// Now cancel and wait
	mgr.CancelAll()
	ok = mgr.WaitWithTimeout(time.Second)
	assert.True(t, ok)
}

func TestBackgroundTaskManager_GetTasksByType(t *testing.T) {
	mgr := agents.NewBackgroundTaskManager(nil)
	defer mgr.Shutdown(time.Second)

	mgr.RegisterFactory(agents.TaskTypePromptSuggestion, func(ctx context.Context, taskCtx agents.BackgroundTaskContext) (interface{}, error) {
		return nil, nil
	})
	mgr.RegisterFactory(agents.TaskTypeMemoryExtraction, func(ctx context.Context, taskCtx agents.BackgroundTaskContext) (interface{}, error) {
		return nil, nil
	})

	mgr.Start(agents.TaskTypePromptSuggestion, agents.BackgroundTaskContext{})
	mgr.Start(agents.TaskTypePromptSuggestion, agents.BackgroundTaskContext{})
	mgr.Start(agents.TaskTypeMemoryExtraction, agents.BackgroundTaskContext{})
	mgr.Wait()

	suggestions := mgr.GetTasksByType(agents.TaskTypePromptSuggestion)
	assert.Len(t, suggestions, 2)

	extractions := mgr.GetTasksByType(agents.TaskTypeMemoryExtraction)
	assert.Len(t, extractions, 1)
}

func TestBackgroundTaskManager_RunEndOfTurnTasks(t *testing.T) {
	mgr := agents.NewBackgroundTaskManager(nil)
	defer mgr.Shutdown(time.Second)

	var count atomic.Int32

	mgr.RegisterFactory(agents.TaskTypePromptSuggestion, func(ctx context.Context, taskCtx agents.BackgroundTaskContext) (interface{}, error) {
		count.Add(1)
		return nil, nil
	})
	mgr.RegisterFactory(agents.TaskTypeMemoryExtraction, func(ctx context.Context, taskCtx agents.BackgroundTaskContext) (interface{}, error) {
		count.Add(1)
		return nil, nil
	})

	mgr.RunEndOfTurnTasks(agents.BackgroundTaskContext{SessionID: "s1"})
	mgr.Wait()

	assert.Equal(t, int32(2), count.Load())
}
