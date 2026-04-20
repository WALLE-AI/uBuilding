package memory

// ---------------------------------------------------------------------------
// M3.I4 · TEXT_FILE_EXTENSIONS whitelist.
//
// Mirrors the 112-entry set at the top of `src/utils/claudemd.ts`.
// @include paths that carry an extension NOT in this set are dropped
// before any file I/O so that a `@foo.png` directive cannot smuggle
// binary data into the prompt.
// ---------------------------------------------------------------------------

// textFileExtensions is a set of lower-case extensions (including the
// leading dot) that are considered safe to @-include into a memory
// file. The list is kept byte-identical to the TS upstream so that a
// diff audit is trivial.
var textFileExtensions = map[string]struct{}{
	// Markdown and text
	".md":   {},
	".txt":  {},
	".text": {},
	// Data formats
	".json": {},
	".yaml": {},
	".yml":  {},
	".toml": {},
	".xml":  {},
	".csv":  {},
	// Web
	".html": {},
	".htm":  {},
	".css":  {},
	".scss": {},
	".sass": {},
	".less": {},
	// JavaScript/TypeScript
	".js":  {},
	".ts":  {},
	".tsx": {},
	".jsx": {},
	".mjs": {},
	".cjs": {},
	".mts": {},
	".cts": {},
	// Python
	".py":  {},
	".pyi": {},
	".pyw": {},
	// Ruby
	".rb":   {},
	".erb":  {},
	".rake": {},
	// Go
	".go": {},
	// Rust
	".rs": {},
	// Java/Kotlin/Scala
	".java":  {},
	".kt":    {},
	".kts":   {},
	".scala": {},
	// C/C++
	".c":   {},
	".cpp": {},
	".cc":  {},
	".cxx": {},
	".h":   {},
	".hpp": {},
	".hxx": {},
	// C#
	".cs": {},
	// Swift
	".swift": {},
	// Shell
	".sh":   {},
	".bash": {},
	".zsh":  {},
	".fish": {},
	".ps1":  {},
	".bat":  {},
	".cmd":  {},
	// Config
	".env":        {},
	".ini":        {},
	".cfg":        {},
	".conf":       {},
	".config":     {},
	".properties": {},
	// Database
	".sql":     {},
	".graphql": {},
	".gql":     {},
	// Protocol
	".proto": {},
	// Frontend frameworks
	".vue":    {},
	".svelte": {},
	".astro":  {},
	// Templating
	".ejs":  {},
	".hbs":  {},
	".pug":  {},
	".jade": {},
	// Other languages
	".php":  {},
	".pl":   {},
	".pm":   {},
	".lua":  {},
	".r":    {},
	".R":    {},
	".dart": {},
	".ex":   {},
	".exs":  {},
	".erl":  {},
	".hrl":  {},
	".clj":  {},
	".cljs": {},
	".cljc": {},
	".edn":  {},
	".hs":   {},
	".lhs":  {},
	".elm":  {},
	".ml":   {},
	".mli":  {},
	".f":    {},
	".f90":  {},
	".f95":  {},
	".for":  {},
	// Build files
	".cmake":    {},
	".make":     {},
	".makefile": {},
	".gradle":   {},
	".sbt":      {},
	// Documentation
	".rst":      {},
	".adoc":     {},
	".asciidoc": {},
	".org":      {},
	".tex":      {},
	".latex":    {},
	// Lock files (often text-based)
	".lock": {},
	// Misc
	".log":   {},
	".diff":  {},
	".patch": {},
}

// IsTextFileExtension reports whether ext (lower-cased, with leading
// dot) is in the @-include whitelist. The empty extension is always
// accepted — upstream treats `@foo` as valid even without a suffix.
func IsTextFileExtension(ext string) bool {
	if ext == "" {
		return true
	}
	_, ok := textFileExtensions[ext]
	return ok
}
