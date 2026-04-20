package memory

import "os"

// osUserHomeDir indirects os.UserHomeDir so tests can stub the
// lookup via homeDirFn. Kept in its own file so the main parser
// doesn't pull "os" beyond this one symbol.
func osUserHomeDir() (string, error) { return os.UserHomeDir() }
