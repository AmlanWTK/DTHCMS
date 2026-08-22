// Command realtime serves the WebSocket gateway that pushes station updates to the
// physician dashboard and the traffic board, so that a value entered on a phone appears
// on another screen without a refresh (blueprint section 4.1).
//
// Separate from the API because long-lived connections scale differently from stateless
// HTTP. The gateway itself arrives at CP26; this binary establishes the deployment shape.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
)

func main() {
	ctx := context.Background()

	rt, err := platform.Boot(ctx, platform.Options{
		Service:    "realtime",
		NeedsCache: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "realtime: cannot start: %v\n", err)
		os.Exit(1)
	}
	defer rt.Close()

	rt.Logger.Info("realtime gateway started",
		"note", "no subscriptions are served yet; the gateway arrives at CP26")

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	rt.Logger.Info("realtime gateway shutting down")
}
