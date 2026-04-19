package fileio

import "github.com/wall-ai/ubuilding/backend/agents/tool"

// resolvePeer returns the name to use inside Prompt() text when referring
// to a peer tool by its canonical name. When the peer tool is not in
// opts.Tools (host filtered it out) we fall back to the canonical name
// so the prompt still reads naturally.
//
// This is the fileio-local counterpart to prompt.CrossRef — we can't
// import the prompt package here without a cycle (prompt ← tool ← fileio).
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
