// Package agents — agent definition loader.
//
// Task A04/A05/A06 · ports loadAgentsDir.ts's markdown + agents.json loader
// plus getActiveAgentsFromList's 5-level priority resolver. Admission rules:
//
//   - Built-in (Go code) comes from DefaultBuiltInAgents().
//   - Markdown: one agent per file, frontmatter parsed as YAML.
//   - JSON: agents.json with map[agentType]AgentJson (same schema as TS).
//   - Priority (low → high): built-in, user, project, plugin, policy.
//     Later sources replace earlier sources for the same agentType.
//
// Loader errors are collected (never abort a whole scan): each malformed file
// becomes an entry in LoadError, mirroring TS's failedFiles[]. Callers can
// surface them to the user via /agents but the engine still boots.
package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// -----------------------------------------------------------------------------
// Public types
// -----------------------------------------------------------------------------

// LoadError describes a single agent-loading failure. Aggregated across a
// directory/file scan so callers can show the user what's broken without
// losing the rest of the valid agents.
type LoadError struct {
	Path   string
	Source AgentSource
	Err    error
}

func (e *LoadError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s (%s): %v", e.Path, e.Source, e.Err)
}

// LoaderConfig controls where the resolver looks. Empty values fall back to
// sensible defaults computed from ProjectRoot / the user's config dir.
type LoaderConfig struct {
	// ProjectRoot is the working directory used to find project-scoped
	// agents (<ProjectRoot>/.claude/...). Defaults to os.Getwd().
	ProjectRoot string

	// UserConfigDir is the base for user-scoped agents
	// (<UserConfigDir>/agents/...). Defaults to $XDG_CONFIG_HOME/ubuilding
	// or %APPDATA%\ubuilding on Windows.
	UserConfigDir string

	// PolicyConfigDir, when non-empty, overrides the policy scan path. Hosts
	// set this for enterprise deployments; default is empty (no policy
	// agents).
	PolicyConfigDir string

	// ExtraSearchPaths lets UBUILDING_AGENTS_PATHS inject additional dirs
	// after the standard roots. Each entry is treated as UserSettings scope.
	ExtraSearchPaths []string

	// IncludeBuiltIn defaults to true. When false, only filesystem agents
	// are returned (useful for isolated tests).
	IncludeBuiltIn bool

	// BuiltInAgents overrides DefaultBuiltInAgents(). Primarily for tests.
	BuiltInAgents []*AgentDefinition
}

// ResolveActiveAgents returns the AgentDefinitions the engine should expose
// this session. The returned struct has:
//   - ActiveAgents: de-duplicated working set (high priority wins per type).
//   - AllAgents:    everything discovered, including shadowed duplicates.
//   - Errors:       aggregated LoadError entries (never nil-filtered).
//
// Phase A keeps the scan strictly filesystem-based. Plugin loading is a
// later milestone (tracked by Phase C tasks).
func ResolveActiveAgents(cfg LoaderConfig) (*AgentDefinitions, []LoadError) {
	if cfg.ProjectRoot == "" {
		if cwd, err := os.Getwd(); err == nil {
			cfg.ProjectRoot = cwd
		}
	}
	if cfg.UserConfigDir == "" {
		cfg.UserConfigDir = defaultUserConfigDir()
	}
	includeBuiltIn := cfg.IncludeBuiltIn || len(cfg.BuiltInAgents) > 0 || !cfg.disableBuiltIn()

	var loadErrs []LoadError
	all := make([]*AgentDefinition, 0, 16)

	appendFrom := func(src AgentSource, dir string) {
		if dir == "" {
			return
		}
		defs, errs := loadAgentsFromDir(dir, src)
		all = append(all, defs...)
		loadErrs = append(loadErrs, errs...)

		jsonDefs, jsonErrs := loadAgentsFromJSONDir(dir, src)
		all = append(all, jsonDefs...)
		loadErrs = append(loadErrs, jsonErrs...)
	}

	// 1. Built-in (lowest priority).
	if includeBuiltIn {
		if len(cfg.BuiltInAgents) > 0 {
			all = append(all, cfg.BuiltInAgents...)
		} else {
			all = append(all, DefaultBuiltInAgents()...)
		}
	}

	// 2. User-scope.
	if cfg.UserConfigDir != "" {
		appendFrom(AgentSourceUser, filepath.Join(cfg.UserConfigDir, "agents"))
	}

	// 3. Project-scope.
	if cfg.ProjectRoot != "" {
		appendFrom(AgentSourceProject, filepath.Join(cfg.ProjectRoot, ".claude", "agents"))
	}

	// 4. Extra search paths (treated as user-scope priority).
	for _, p := range cfg.ExtraSearchPaths {
		appendFrom(AgentSourceUser, p)
	}

	// 5. Policy-scope (highest priority).
	if cfg.PolicyConfigDir != "" {
		appendFrom(AgentSourcePolicy, filepath.Join(cfg.PolicyConfigDir, "agents"))
	}

	out := &AgentDefinitions{
		AllAgents:    all,
		ActiveAgents: deduplicateByPriority(all),
	}
	out.RefreshLegacy()
	return out, loadErrs
}

// disableBuiltIn is a cfg-level override mirroring DefaultBuiltInAgents()'s
// env check. When both cfg.BuiltInAgents is empty and IncludeBuiltIn is
// false AND UBUILDING_DISABLE_BUILTIN_AGENTS is truthy, skip built-ins.
func (c LoaderConfig) disableBuiltIn() bool {
	if isEnvTruthy(os.Getenv("UBUILDING_DISABLE_BUILTIN_AGENTS")) {
		return true
	}
	return false
}

// -----------------------------------------------------------------------------
// Priority resolution
// -----------------------------------------------------------------------------

// sourcePriority assigns an ordinal to each source — higher replaces lower
// on duplicate agentType. Matches getActiveAgentsFromList ordering.
func sourcePriority(s AgentSource) int {
	switch s {
	case AgentSourceBuiltIn:
		return 0
	case AgentSourceUser:
		return 1
	case AgentSourceProject:
		return 2
	case AgentSourcePlugin:
		return 3
	case AgentSourcePolicy:
		return 4
	default:
		return -1
	}
}

// deduplicateByPriority returns a new slice keeping, for each agentType,
// the definition with the highest priority. Stable in insertion order
// within the winning priority tier.
func deduplicateByPriority(defs []*AgentDefinition) []*AgentDefinition {
	type slot struct {
		def *AgentDefinition
		ord int // insertion order, for stable fallback
	}
	byType := make(map[string]slot, len(defs))
	for i, d := range defs {
		if d == nil || d.AgentType == "" {
			continue
		}
		cur, ok := byType[d.AgentType]
		if !ok {
			byType[d.AgentType] = slot{def: d, ord: i}
			continue
		}
		curPrio := sourcePriority(cur.def.Source)
		newPrio := sourcePriority(d.Source)
		if newPrio > curPrio {
			byType[d.AgentType] = slot{def: d, ord: i}
		}
	}
	// Sort by insertion order for determinism.
	sorted := make([]slot, 0, len(byType))
	for _, s := range byType {
		sorted = append(sorted, s)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ord < sorted[j].ord })
	out := make([]*AgentDefinition, 0, len(sorted))
	for _, s := range sorted {
		out = append(out, s.def)
	}
	return out
}

// -----------------------------------------------------------------------------
// Markdown loader (A04)
// -----------------------------------------------------------------------------

// markdownFrontmatter mirrors the yaml schema used by claude-code agents.
// Unknown fields are tolerated because yaml.v3's default decoder is lenient.
type markdownFrontmatter struct {
	Description                        string             `yaml:"description"`
	Name                               string             `yaml:"name"`
	Tools                              flexibleStringList `yaml:"tools"`
	DisallowedTools                    flexibleStringList `yaml:"disallowedTools"`
	Skills                             flexibleStringList `yaml:"skills"`
	Model                              string             `yaml:"model"`
	Effort                             *yaml.Node         `yaml:"effort"`
	PermissionMode                     string             `yaml:"permissionMode"`
	MaxTurns                           int                `yaml:"maxTurns"`
	InitialPrompt                      string             `yaml:"initialPrompt"`
	Memory                             string             `yaml:"memory"`
	Background                         bool               `yaml:"background"`
	Isolation                          string             `yaml:"isolation"`
	Color                              string             `yaml:"color"`
	OmitClaudeMd                       bool               `yaml:"omitClaudeMd"`
	RequiredMcpServers                 flexibleStringList `yaml:"requiredMcpServers"`
	MCPServers                         []interface{}      `yaml:"mcpServers"`
	Hooks                              interface{}        `yaml:"hooks"`
	CriticalSystemReminderExperimental string             `yaml:"criticalSystemReminder_EXPERIMENTAL"`
}

// flexibleStringList lets frontmatter fields be either a comma-separated
// string ("Read, Grep") or a YAML list. Matches parseAgentToolsFromFrontmatter.
type flexibleStringList []string

func (f *flexibleStringList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		var list []string
		if err := node.Decode(&list); err != nil {
			return err
		}
		*f = trimAndFilter(list)
		return nil
	case yaml.ScalarNode:
		var s string
		if err := node.Decode(&s); err != nil {
			return err
		}
		*f = trimAndFilter(strings.Split(s, ","))
		return nil
	case 0:
		return nil
	default:
		return fmt.Errorf("expected list or string for frontmatter field, got kind=%d", node.Kind)
	}
}

func trimAndFilter(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// parseFrontmatter splits `---\n<yaml>\n---\n<body>` content. Returns empty
// yaml + whole content when no frontmatter is present — matches TS which
// allows prompt-only agents (description then derives from filename).
func parseFrontmatter(content string) (yamlBlock, body string, hasFrontmatter bool) {
	const sep = "---"
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, sep) {
		return "", content, false
	}
	// Skip the leading '---' line.
	rest := strings.TrimPrefix(trimmed, sep)
	// Support both \n and \r\n after the opening marker.
	rest = strings.TrimLeft(rest, "\r\n")

	// Look for the closing '---' on its own line.
	end := strings.Index(rest, "\n"+sep)
	if end < 0 {
		end = strings.Index(rest, "\r\n"+sep)
	}
	if end < 0 {
		return "", content, false
	}
	yamlBlock = rest[:end]
	bodyStart := end
	// Advance past the line break and the closing '---'.
	for bodyStart < len(rest) && (rest[bodyStart] == '\n' || rest[bodyStart] == '\r') {
		bodyStart++
	}
	bodyStart += len(sep)
	body = rest[bodyStart:]
	// Eat one trailing newline after the closing marker.
	body = strings.TrimPrefix(body, "\r\n")
	body = strings.TrimPrefix(body, "\n")
	return yamlBlock, body, true
}

// LoadAgentFromMarkdown parses a single .md agent file into an AgentDefinition.
// Callers pass the source (e.g. project/user/policy) so priority resolution
// works correctly. filename (without extension) is used as the fallback
// agentType when frontmatter is missing a `name` field.
func LoadAgentFromMarkdown(path string, source AgentSource) (*AgentDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseMarkdownAgent(path, source, string(data))
}

func parseMarkdownAgent(path string, source AgentSource, content string) (*AgentDefinition, error) {
	yamlBlock, body, hasFM := parseFrontmatter(content)
	var fm markdownFrontmatter
	if hasFM && strings.TrimSpace(yamlBlock) != "" {
		if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
			return nil, fmt.Errorf("parse frontmatter: %w", err)
		}
	}
	filename := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	agentType := firstNonEmpty(fm.Name, filename)
	if agentType == "" {
		return nil, errors.New("agent type is empty (no name frontmatter, no filename)")
	}
	body = strings.TrimSpace(body)
	if fm.Description == "" {
		return nil, errors.New("description frontmatter is required")
	}
	if body == "" {
		return nil, errors.New("prompt body is empty")
	}
	def := &AgentDefinition{
		AgentType:                          agentType,
		WhenToUse:                          fm.Description,
		Tools:                              []string(fm.Tools),
		DisallowedTools:                    []string(fm.DisallowedTools),
		Skills:                             []string(fm.Skills),
		Model:                              strings.TrimSpace(fm.Model),
		Effort:                             parseEffortFromYAML(fm.Effort),
		PermissionMode:                     strings.TrimSpace(fm.PermissionMode),
		MaxTurns:                           fm.MaxTurns,
		InitialPrompt:                      fm.InitialPrompt,
		Memory:                             AgentMemoryScope(strings.TrimSpace(fm.Memory)),
		Background:                         fm.Background,
		Isolation:                          AgentIsolation(strings.TrimSpace(fm.Isolation)),
		Color:                              strings.TrimSpace(fm.Color),
		OmitClaudeMd:                       fm.OmitClaudeMd,
		RequiredMcpServers:                 []string(fm.RequiredMcpServers),
		MCPServers:                         parseMCPServers(fm.MCPServers),
		Hooks:                              fm.Hooks,
		CriticalSystemReminderExperimental: fm.CriticalSystemReminderExperimental,
		Source:                             source,
		BaseDir:                            filepath.Dir(path),
		Filename:                           filename,
		GetSystemPrompt: func(body string) GetSystemPromptFn {
			return func(_ SystemPromptCtx) string { return body }
		}(body),
	}
	return def, nil
}

// parseEffortFromYAML accepts either a string ("low") or integer effort.
func parseEffortFromYAML(node *yaml.Node) AgentEffortValue {
	if node == nil {
		return AgentEffortValue{}
	}
	switch node.Kind {
	case yaml.ScalarNode:
		var s string
		if err := node.Decode(&s); err == nil && s != "" {
			return AgentEffortValue{Name: strings.TrimSpace(s)}
		}
		var i int
		if err := node.Decode(&i); err == nil {
			return AgentEffortValue{Value: i}
		}
	}
	return AgentEffortValue{}
}

// parseMCPServers normalises the YAML shape of mcpServers into our Go
// AgentMcpServerSpec list. Accepted list entries mirror loadAgentsDir.ts:
//
//   - "server-name"               # string reference (use parent config)
//   - {server-name: {...}}         # single-key map → inline definition
//
// JSON callers go through parseMCPServersFromJSON (stricter types) before
// re-entering here via the interface slice path.
func parseMCPServers(raw []interface{}) []AgentMcpServerSpec {
	if len(raw) == 0 {
		return nil
	}
	out := make([]AgentMcpServerSpec, 0, len(raw))
	for _, entry := range raw {
		switch v := entry.(type) {
		case string:
			if v != "" {
				out = append(out, AgentMcpServerSpec{ByName: v})
			}
		case map[string]interface{}:
			for k, val := range v {
				if m, ok := val.(map[string]interface{}); ok {
					out = append(out, AgentMcpServerSpec{Inline: map[string]interface{}{k: m}})
				} else if s, ok := val.(string); ok && s != "" {
					out = append(out, AgentMcpServerSpec{ByName: s})
				} else {
					out = append(out, AgentMcpServerSpec{ByName: k})
				}
			}
		case map[interface{}]interface{}:
			// yaml.v3 with `interface{}` target uses string keys, but be
			// defensive for any caller that decoded into the legacy shape.
			for rawKey, val := range v {
				k, _ := rawKey.(string)
				if k == "" {
					continue
				}
				if m, ok := val.(map[string]interface{}); ok {
					out = append(out, AgentMcpServerSpec{Inline: map[string]interface{}{k: m}})
				} else {
					out = append(out, AgentMcpServerSpec{ByName: k})
				}
			}
		}
	}
	return out
}

// loadAgentsFromDir walks dir for *.md files (non-recursive, matching TS
// behaviour) and returns parsed defs + per-file errors.
func loadAgentsFromDir(dir string, source AgentSource) ([]*AgentDefinition, []LoadError) {
	var defs []*AgentDefinition
	var errs []LoadError
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		errs = append(errs, LoadError{Path: dir, Source: source, Err: err})
		return nil, errs
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		def, err := LoadAgentFromMarkdown(full, source)
		if err != nil {
			errs = append(errs, LoadError{Path: full, Source: source, Err: err})
			continue
		}
		defs = append(defs, def)
	}
	return defs, errs
}

// -----------------------------------------------------------------------------
// JSON loader (A05)
// -----------------------------------------------------------------------------

// AgentJSONEntry is the shape of a single entry in agents.json. Optional
// fields use pointer-or-zero semantics so missing keys remain unset.
type AgentJSONEntry struct {
	Description                        string        `json:"description"`
	Prompt                             string        `json:"prompt"`
	Tools                              []string      `json:"tools,omitempty"`
	DisallowedTools                    []string      `json:"disallowedTools,omitempty"`
	Skills                             []string      `json:"skills,omitempty"`
	Model                              string        `json:"model,omitempty"`
	Effort                             interface{}   `json:"effort,omitempty"`
	PermissionMode                     string        `json:"permissionMode,omitempty"`
	MaxTurns                           int           `json:"maxTurns,omitempty"`
	InitialPrompt                      string        `json:"initialPrompt,omitempty"`
	Memory                             string        `json:"memory,omitempty"`
	Background                         bool          `json:"background,omitempty"`
	Isolation                          string        `json:"isolation,omitempty"`
	Color                              string        `json:"color,omitempty"`
	OmitClaudeMd                       bool          `json:"omitClaudeMd,omitempty"`
	RequiredMcpServers                 []string      `json:"requiredMcpServers,omitempty"`
	MCPServers                         []interface{} `json:"mcpServers,omitempty"`
	Hooks                              interface{}   `json:"hooks,omitempty"`
	CriticalSystemReminderExperimental string        `json:"criticalSystemReminder_EXPERIMENTAL,omitempty"`
}

// LoadAgentsFromJSON parses one agents.json file into a list of definitions.
// Invalid entries produce LoadError entries and are skipped; valid entries
// are returned.
func LoadAgentsFromJSON(path string, source AgentSource) ([]*AgentDefinition, []LoadError) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []LoadError{{Path: path, Source: source, Err: err}}
	}
	return parseJSONAgents(path, source, data)
}

func parseJSONAgents(path string, source AgentSource, data []byte) ([]*AgentDefinition, []LoadError) {
	var doc map[string]AgentJSONEntry
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, []LoadError{{Path: path, Source: source, Err: fmt.Errorf("parse agents.json: %w", err)}}
	}
	var defs []*AgentDefinition
	var errs []LoadError
	// Sort keys for determinism.
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, agentType := range keys {
		entry := doc[agentType]
		if strings.TrimSpace(entry.Description) == "" {
			errs = append(errs, LoadError{Path: path, Source: source, Err: fmt.Errorf("%s: description required", agentType)})
			continue
		}
		if strings.TrimSpace(entry.Prompt) == "" {
			errs = append(errs, LoadError{Path: path, Source: source, Err: fmt.Errorf("%s: prompt required", agentType)})
			continue
		}
		def := &AgentDefinition{
			AgentType:                          agentType,
			WhenToUse:                          entry.Description,
			Tools:                              entry.Tools,
			DisallowedTools:                    entry.DisallowedTools,
			Skills:                             entry.Skills,
			Model:                              entry.Model,
			Effort:                             parseEffortFromInterface(entry.Effort),
			PermissionMode:                     entry.PermissionMode,
			MaxTurns:                           entry.MaxTurns,
			InitialPrompt:                      entry.InitialPrompt,
			Memory:                             AgentMemoryScope(entry.Memory),
			Background:                         entry.Background,
			Isolation:                          AgentIsolation(entry.Isolation),
			Color:                              entry.Color,
			OmitClaudeMd:                       entry.OmitClaudeMd,
			RequiredMcpServers:                 entry.RequiredMcpServers,
			MCPServers:                         parseMCPServers(entry.MCPServers),
			Hooks:                              entry.Hooks,
			CriticalSystemReminderExperimental: entry.CriticalSystemReminderExperimental,
			Source:                             source,
			BaseDir:                            filepath.Dir(path),
			Filename:                           agentType,
			GetSystemPrompt: func(body string) GetSystemPromptFn {
				return func(_ SystemPromptCtx) string { return body }
			}(entry.Prompt),
		}
		defs = append(defs, def)
	}
	return defs, errs
}

func parseEffortFromInterface(v interface{}) AgentEffortValue {
	switch x := v.(type) {
	case string:
		if x != "" {
			return AgentEffortValue{Name: x}
		}
	case float64:
		return AgentEffortValue{Value: int(x)}
	case int:
		return AgentEffortValue{Value: x}
	}
	return AgentEffortValue{}
}

// loadAgentsFromJSONDir looks for a single agents.json inside dir. Matches
// TS which uses a convention-over-configuration single file.
func loadAgentsFromJSONDir(dir string, source AgentSource) ([]*AgentDefinition, []LoadError) {
	candidate := filepath.Join(dir, "agents.json")
	if _, err := os.Stat(candidate); err != nil {
		return nil, nil
	}
	return LoadAgentsFromJSON(candidate, source)
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func defaultUserConfigDir() string {
	if v := strings.TrimSpace(os.Getenv("UBUILDING_CONFIG_DIR")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); v != "" {
		return filepath.Join(v, "ubuilding")
	}
	if home, err := os.UserConfigDir(); err == nil && home != "" {
		return filepath.Join(home, "ubuilding")
	}
	return ""
}
