package bash

import "github.com/wall-ai/ubuilding/backend/agents/tool"

// resolvePeer returns the registered name for a peer tool, falling back
// to the canonical primary when that peer is not present in opts.Tools.
// Same pattern as fileio/glob/grep; kept local to avoid importing the
// prompt package (which depends on tool).
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
