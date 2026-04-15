package agents_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wall-ai/ubuilding/backend/agents"
)

func TestCommandRegistry_RegisterAndFind(t *testing.T) {
	reg := agents.NewCommandRegistry()
	reg.Register(agents.Command{
		Name:        "test",
		Aliases:     []string{"t"},
		Description: "a test command",
		Type:        agents.CommandTypeLocal,
	})

	assert.NotNil(t, reg.Find("test"))
	assert.NotNil(t, reg.Find("t")) // alias
	assert.Nil(t, reg.Find("unknown"))
}

func TestCommandRegistry_GetEnabled(t *testing.T) {
	reg := agents.NewCommandRegistry()
	reg.Register(agents.Command{Name: "always", Type: agents.CommandTypeLocal})
	reg.Register(agents.Command{
		Name: "disabled",
		Type: agents.CommandTypeLocal,
		IsEnabled: func() bool { return false },
	})
	reg.Register(agents.Command{Name: "also-always", Type: agents.CommandTypeLocal})

	enabled := reg.GetEnabled()
	assert.Len(t, enabled, 2)
}

func TestCommandRegistry_GetVisible(t *testing.T) {
	reg := agents.NewCommandRegistry()
	reg.Register(agents.Command{Name: "visible", Type: agents.CommandTypeLocal})
	reg.Register(agents.Command{Name: "hidden", Type: agents.CommandTypeLocal, IsHidden: true})

	visible := reg.GetVisible()
	assert.Len(t, visible, 1)
	assert.Equal(t, "visible", visible[0].Name)
}

func TestParseSlashCommand(t *testing.T) {
	reg := agents.NewCommandRegistry()
	reg.Register(agents.Command{Name: "clear", Type: agents.CommandTypeLocal})
	reg.Register(agents.Command{Name: "compact", Aliases: []string{"c"}, Type: agents.CommandTypeLocal})

	tests := []struct {
		input    string
		wantNil  bool
		wantName string
		wantArgs string
		wantCmd  bool
	}{
		{"hello world", true, "", "", false},
		{"/clear", false, "clear", "", true},
		{"/compact some args", false, "compact", "some args", true},
		{"/c", false, "c", "", true},          // alias
		{"/unknown", false, "unknown", "", false}, // parsed but no command
		{"/", true, "", "", false},             // just slash
		{"  /clear  ", false, "clear", "", true}, // trimmed
	}

	for _, tt := range tests {
		parsed := agents.ParseSlashCommand(tt.input, reg)
		if tt.wantNil {
			assert.Nil(t, parsed, "input=%q", tt.input)
			continue
		}
		require.NotNil(t, parsed, "input=%q", tt.input)
		assert.Equal(t, tt.wantName, parsed.Name, "input=%q", tt.input)
		assert.Equal(t, tt.wantArgs, parsed.Args, "input=%q", tt.input)
		if tt.wantCmd {
			assert.NotNil(t, parsed.Command, "input=%q", tt.input)
		} else {
			assert.Nil(t, parsed.Command, "input=%q", tt.input)
		}
	}
}

func TestBuiltinCommands_Clear(t *testing.T) {
	reg := agents.NewCommandRegistry()
	agents.RegisterBuiltinCommands(reg)

	cmd := reg.Find("clear")
	require.NotNil(t, cmd)

	var cleared bool
	result, err := cmd.Call("", agents.CommandContext{
		Ctx:      context.Background(),
		Messages: []agents.Message{{Type: agents.MessageTypeUser}},
		SetMessages: func(fn func([]agents.Message) []agents.Message) {
			msgs := fn([]agents.Message{{Type: agents.MessageTypeUser}})
			cleared = len(msgs) == 0
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "text", result.Type)
	assert.True(t, cleared)
}

func TestBuiltinCommands_Compact(t *testing.T) {
	reg := agents.NewCommandRegistry()
	agents.RegisterBuiltinCommands(reg)

	cmd := reg.Find("compact")
	require.NotNil(t, cmd)
	// Also reachable via alias
	assert.NotNil(t, reg.Find("c"))

	result, err := cmd.Call("", agents.CommandContext{Ctx: context.Background()})
	require.NoError(t, err)
	assert.Equal(t, "compact", result.Type)
}

func TestBuiltinCommands_Help(t *testing.T) {
	reg := agents.NewCommandRegistry()
	agents.RegisterBuiltinCommands(reg)

	cmd := reg.Find("help")
	require.NotNil(t, cmd)
	assert.NotNil(t, reg.Find("?")) // alias

	result, err := cmd.Call("", agents.CommandContext{Ctx: context.Background()})
	require.NoError(t, err)
	assert.Equal(t, "text", result.Type)
	assert.Contains(t, result.Value, "Available commands")
}

func TestBuiltinCommands_Exit(t *testing.T) {
	reg := agents.NewCommandRegistry()
	agents.RegisterBuiltinCommands(reg)

	cmd := reg.Find("exit")
	require.NotNil(t, cmd)
	assert.NotNil(t, reg.Find("quit"))
	assert.NotNil(t, reg.Find("q"))

	result, err := cmd.Call("", agents.CommandContext{Ctx: context.Background()})
	require.NoError(t, err)
	assert.Equal(t, "skip", result.Type)
}
