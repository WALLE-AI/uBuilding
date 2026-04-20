// Package memory implements the full CLAUDE.md / MEMORY.md / team-memory
// subsystem ported from claude-code-main's `src/utils/claudemd.ts`,
// `src/memdir/*` and `src/utils/memoryFileDetection.ts`.
//
// Sub-areas:
//
//   - types.go / paths.go : data model + path resolution (M1, M2).
//   - claudemd.go / claudemd_parse.go / claudemd_rules.go :
//     layered CLAUDE.md loader with @include, frontmatter, HTML-comment
//     stripping, `.claude/rules/` and external-include gating (M3).
//   - memdir.go : MEMORY.md entrypoint (auto-memory) (M4).
//   - memory_types.go : 4-tier taxonomy prompt constants (M5).
//   - team_paths.go : team memory path validation (M6).
//   - memory_prompt.go : prompt assembly (M7).
//   - render.go : BuildUserContextClaudeMd + GetLargeMemoryFiles (M8).
//   - detection.go : file/directory/shell-command classification
//     predicates for tool permission and collapse/badge logic (M9).
//   - memory_scan.go : ScanMemoryFiles + FormatMemoryManifest (M10).
//   - memory_age.go : freshness helpers (MemoryAge, MemoryFreshnessNote) (M11).
//   - find_relevant.go : query-time recall via injectable LLM side-query (M12).
//   - secret_scanner.go : gitleaks-based secret detection (M13).
//   - write_allowlist.go : IsAutoMemWriteAllowed + CheckTeamMemSecrets (M13).
//
// Compatibility: every new capability is opt-in via `EngineConfig`
// fields and `UBUILDING_*` environment variables and defaults to off so
// existing callers are not affected.
//
// Dependencies on the `agents` package are one-way: this package imports
// `agents` for `Message` / `AgentMemoryScope`, and `agents` must NEVER
// import `memory` directly — integration happens through callable fields
// on `EngineConfig`.
package memory
