package agenttool

import "github.com/wall-ai/ubuilding/backend/agents/tool"

// resolvePeer returns the registered name for a peer tool, falling back
// to the canonical primary when that peer is absent from opts.Tools.
// Same pattern used across askuser/fileio/glob/grep/bash/powershell packages.
func resolvePeer(opts tool.PromptOptions, primary string) string {
	if len(opts.Tools) == 0 {
		return primary
	}
	for _, t := range opts.Tools {
		if t == nil {
			continue
		}
		if t.Name() == primary {
			return primary
		}
		for _, alias := range t.Aliases() {
			if alias == primary {
				return t.Name()
			}
		}
	}
	return primary
}
