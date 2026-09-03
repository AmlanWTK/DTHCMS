// Command accessmatrix prints docs/access-matrix.md from the RBAC engine.
//
//	go run ./tools/accessmatrix > ../docs/access-matrix.md
package main

import (
	"fmt"

	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

func main() {
	fmt.Print(rbac.RenderMatrix())
}
