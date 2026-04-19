package glob

import "github.com/wall-ai/ubuilding/backend/agents/tool"

// resolvePeer returns the name to use when referring to a peer tool in
// Prompt() output. It falls back to the canonical primary name when the
// tool is absent from opts.Tools (host filtered it out or renamed it).
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
