package grep

import "github.com/wall-ai/ubuilding/backend/agents/tool"

// resolvePeer falls back to the canonical name when the peer tool is not
// present in opts.Tools. Same pattern as fileio/glob helpers; kept local
// to avoid a cross-package cycle (prompt → tool → grep).
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
