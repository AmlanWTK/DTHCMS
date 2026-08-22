// Command api serves the DTHCMS HTTP API.
//
// At CP05 it serves only the operational endpoints — /healthz, /readyz, /version — and
// an authenticated /v1 namespace whose middleware chain is wired but whose links are
// placeholders. Clinical routes arrive with the modules that own them.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/config"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/version"
)

func main() {
	if code := run(); code != 0 {
		os.Exit(code)
	}
}

func run() int {
	ctx := context.Background()

	rt, err := platform.Boot(ctx, platform.Options{
		Service:    "api",
		NeedsDB:    true,
		NeedsCache: true,
	})
	if err != nil {
		// Configuration problems are printed plainly: at this point there is no logger,
		// and the person reading this is trying to fix a deployment.
		fmt.Fprintf(os.Stderr, "api: cannot start: %v\n", err)
		if config.IsInvalid(err) {
			fmt.Fprintln(os.Stderr, "\nFix the settings above and start again. "+
				"Nothing is served until configuration is valid.")
		}
		return 1
	}
	defer rt.Close()

	build := version.Current()

	health := &httpx.Health{
		Service:   "api",
		Version:   build.Version,
		Commit:    build.Commit,
		BuildTime: build.BuildTime,
		Logger:    rt.Logger,
		Timeout:   3 * time.Second,
		Dependencies: []httpx.Dependency{
			{Name: "postgres", Check: rt.DB.Check, Critical: true},
			{Name: "redis", Check: rt.Cache.Check, Critical: true},
			{Name: "blobstore", Check: rt.Blob.Check, Critical: false},
		},
	}

	router := httpx.NewRouter(httpx.RouterOptions{
		Logger:         rt.Logger,
		IDs:            rt.IDs,
		AllowedOrigins: rt.Config.HTTP.AllowedOrigins,
		MaxBodyBytes:   rt.Config.HTTP.MaxBodyBytes,
		RequestTimeout: rt.Config.HTTP.WriteTimeout,
		Health:         health,
	})

	err = httpx.Serve(ctx, httpx.ServerOptions{
		Addr:            rt.Config.HTTP.Addr,
		Handler:         router,
		Logger:          rt.Logger,
		ReadTimeout:     rt.Config.HTTP.ReadTimeout,
		WriteTimeout:    rt.Config.HTTP.WriteTimeout,
		IdleTimeout:     rt.Config.HTTP.IdleTimeout,
		ShutdownTimeout: rt.Config.HTTP.ShutdownTimeout,
	})
	if err != nil {
		rt.Logger.Error("server stopped with an error", "error", err.Error())
		return 1
	}
	return 0
}
