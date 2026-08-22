// Package version reports build information for DTHCMS binaries.
//
// Values are overridden at build time:
//
//	go build -ldflags "-X github.com/arrowhealth/dthcms/backend/internal/version.commit=$(git rev-parse HEAD)"
package version

import "fmt"

var (
	// version is the semantic version of the build.
	version = "0.1.0-cp01"
	// commit is the git commit the binary was built from.
	commit = "unknown"
	// buildTime is the RFC3339 timestamp of the build.
	buildTime = "unknown"
)

// Info describes a build.
type Info struct {
	Version   string
	Commit    string
	BuildTime string
}

// Current returns the build information for this binary.
func Current() Info {
	return Info{Version: version, Commit: commit, BuildTime: buildTime}
}

// String renders the build information for logs and the version endpoint.
func String() string {
	i := Current()
	return fmt.Sprintf("v%s (commit %s, built %s)", i.Version, i.Commit, i.BuildTime)
}
