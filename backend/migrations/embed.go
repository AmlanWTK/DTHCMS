// Package migrations holds the SQL migrations and embeds them into the binary.
//
// Embedding is deliberate. A migrate binary that reads .sql files from disk applies
// whatever happens to be in that directory on the machine it runs on, which is not
// necessarily the code that was reviewed. Embedded, the migrations and the binary are
// one artefact: the version that passed CI is the version that runs.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
