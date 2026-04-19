package mcp

import (
	"strings"
	"testing"

	"github.com/wall-ai/ubuilding/backend/agents/tool"
)

func TestListMcpResources_DescriptionMirrorsUpstream(t *testing.T) {
	d := NewListTool().Description(nil)
	for _, want := range []string{
		"Lists available resources from configured MCP servers.",
		"Each resource object includes a 'server' field",
		"Usage examples:",
		"`listMcpResources`",
		`listMcpResources({ server: "myserver" })`,
	} {
		if !strings.Contains(d, want) {
			t.Errorf("ListMcpResources description missing %q", want)
		}
	}
}

func TestListMcpResources_PromptMirrorsUpstream(t *testing.T) {
	p := NewListTool().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"List available resources from configured MCP servers.",
		"standard MCP resource fields plus a 'server' field",
		"Parameters:",
		"server (optional):",
		"If not provided,",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("ListMcpResources prompt missing %q", want)
		}
	}
}

func TestReadMcpResource_DescriptionMirrorsUpstream(t *testing.T) {
	d := NewReadTool().Description(nil)
	for _, want := range []string{
		"Reads a specific resource from an MCP server.",
		"server: The name of the MCP server to read from",
		"uri: The URI of the resource to read",
		"Usage examples:",
		`readMcpResource({ server: "myserver", uri: "my-resource-uri" })`,
	} {
		if !strings.Contains(d, want) {
			t.Errorf("ReadMcpResource description missing %q", want)
		}
	}
}

func TestReadMcpResource_PromptMirrorsUpstream(t *testing.T) {
	p := NewReadTool().Prompt(tool.PromptOptions{})
	for _, want := range []string{
		"Reads a specific resource from an MCP server, identified by server name and resource URI.",
		"Parameters:",
		"server (required):",
		"uri (required):",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("ReadMcpResource prompt missing %q", want)
		}
	}
}
