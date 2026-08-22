// Command api is the DTHCMS HTTP API server.
//
// CP01 scope: this binary exists to prove the module builds and that CI runs it.
// It has no HTTP server, no configuration, no database and no domain logic — those
// arrive at CP05 (platform layer) and CP06 (database foundation).
package main

import (
	"fmt"
	"os"

	"github.com/arrowhealth/dthcms/backend/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version.String())
		return
	}

	fmt.Printf("DTHCMS API %s\n", version.String())
	fmt.Println("No server is started yet — the platform layer arrives at CP05.")
}
