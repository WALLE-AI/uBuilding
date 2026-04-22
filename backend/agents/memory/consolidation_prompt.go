package memory

import "fmt"

// ---------------------------------------------------------------------------
// M14.I3 · Consolidation prompt — structured 4-phase prompt for the
// auto-dream memory consolidation agent.
//
// Ports `src/services/autoDream/consolidationPrompt.ts`.
// ---------------------------------------------------------------------------

// BuildConsolidationPrompt returns the system prompt for the auto-dream
// consolidation agent. The prompt guides the model through a structured
// 4-phase workflow: Orient → Gather → Consolidate → Prune.
//
// Parameters:
//   - memoryRoot:    absolute path to the auto-memory directory.
//   - transcriptDir: absolute path to the session transcript directory.
//   - extra:         optional additional context appended to the prompt;
//     pass "" to omit.
func BuildConsolidationPrompt(memoryRoot, transcriptDir, extra string) string {
	prompt := fmt.Sprintf(`# Dream: Memory Consolidation

You are performing a dream — a reflective pass over your memory files. Synthesize what you've learned recently into durable, well-organized memories so that future sessions can orient quickly.

Memory directory: `+"`%s`"+`
%s

Session transcripts: `+"`%s`"+` (large JSONL files — grep narrowly, don't read whole files)

---

## Phase 1 — Orient

- `+"`ls`"+` the memory directory to see what already exists
- Read `+"`%s`"+` to understand the current index
- Skim existing topic files so you improve them rather than creating duplicates
- If `+"`logs/`"+` or `+"`sessions/`"+` subdirectories exist (assistant-mode layout), review recent entries there

## Phase 2 — Gather recent signal

Look for new information worth persisting. Sources in rough priority order:

1. **Daily logs** (`+"`logs/YYYY/MM/YYYY-MM-DD.md`"+`) if present — these are the append-only stream
2. **Existing memories that drifted** — facts that contradict something you see in the codebase now
3. **Transcript search** — if you need specific context (e.g., "what was the error message from yesterday's build failure?"), grep the JSONL transcripts for narrow terms:
   `+"`"+`grep -rn "<narrow term>" %s/ --include="*.jsonl" | tail -50`+"`"+`

Don't exhaustively read transcripts. Look only for things you already suspect matter.

## Phase 3 — Consolidate

For each thing worth remembering, write or update a memory file at the top level of the memory directory. Use the memory file format and type conventions from your system prompt's auto-memory section — it's the source of truth for what to save, how to structure it, and what NOT to save.

Focus on:
- Merging new signal into existing topic files rather than creating near-duplicates
- Converting relative dates ("yesterday", "last week") to absolute dates so they remain interpretable after time passes
- Deleting contradicted facts — if today's investigation disproves an old memory, fix it at the source

## Phase 4 — Prune and index

Update `+"`%s`"+` so it stays under %d lines AND under ~25KB. It's an **index**, not a dump — each entry should be one line under ~150 characters: `+"`- [Title](file.md) — one-line hook`"+`. Never write memory content directly into it.

- Remove pointers to memories that are now stale, wrong, or superseded
- Demote verbose entries: if an index line is over ~200 chars, it's carrying content that belongs in the topic file — shorten the line, move the detail
- Add pointers to newly important memories
- Resolve contradictions — if two files disagree, fix the wrong one

---

Return a brief summary of what you consolidated, updated, or pruned. If nothing changed (memories are already tight), say so.`,
		memoryRoot,
		DirExistsGuidance,
		transcriptDir,
		autoMemEntrypoint,
		transcriptDir,
		autoMemEntrypoint,
		MaxEntrypointLines,
	)

	if extra != "" {
		prompt += "\n\n## Additional context\n\n" + extra
	}

	return prompt
}
