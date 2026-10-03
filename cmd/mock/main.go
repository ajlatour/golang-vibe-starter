// Command mock runs a stand-in upstream for the API: it serves the /items
// resource from memory, pre-seeded with sample data. Data resets on restart.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/ajlatour/golang-vibe-starter/internal/httpx"
	"github.com/ajlatour/golang-vibe-starter/internal/item"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first signal, restore default handling so a second one kills
	// the process immediately instead of waiting for graceful shutdown.
	go func() {
		<-ctx.Done()
		stop()
	}()

	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// seedItems is the sample data the mock starts with.
var seedItems = []item.Item{
	{Name: "Widget", Description: "A standard widget."},
	{Name: "Gadget", Description: "A gadget with extra features."},
	{Name: "Gizmo"},
}

// run wires up and runs the service until ctx is cancelled. With the single
// argument "healthcheck" it instead probes an already-running instance, for
// use as a container healthcheck.
func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	cfg, err := httpx.LoadServerConfig(getenv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "healthcheck":
		return httpx.Probe(ctx, cfg.Addr)
	default:
		return fmt.Errorf("unexpected arguments %q (want none, or \"healthcheck\")", args)
	}

	logger := cfg.NewLogger(stdout)
	store := item.NewMemStore()
	for _, it := range seedItems {
		if _, err := store.Create(ctx, it); err != nil {
			return fmt.Errorf("seed: %w", err)
		}
	}
	logger.Info("seeded items", "count", len(seedItems))

	return httpx.Serve(ctx, logger, cfg, newHandler(logger, store))
}

// newHandler builds the mock's root handler: routes plus middleware.
func newHandler(logger *slog.Logger, items item.Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	item.RegisterRoutes(mux, logger, items)
	return httpx.Middleware(logger, mux)
}
