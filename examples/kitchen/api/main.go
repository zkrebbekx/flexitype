// Command kitchen is the API behind a restaurant group's recipe and menu
// system, built on a standalone flexitype over HTTP with the Go SDK.
//
// It is a SINGLE-TENANT example on purpose. Nothing here is about tenancy: the
// point is what the service computes on its own.
//
// The costing is entirely the service's work. This application writes an
// ingredient's pack price and a recipe line's quantity, and nothing else:
//
//	ingredient.cost_per_kg       formula  pack_price / pack_size
//	recipe_line.ingredient_cost  rollup   sum(parent(of_ingredient).cost_per_kg)
//	recipe_line.line_cost        formula  quantity * ingredient_cost
//	dish.food_cost               rollup   sum(child(has_line).line_cost)
//
// A supplier raises a price, and every dish that uses that ingredient recosts
// itself two relationships away. There is no code here that adds up a dish.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zkrebbekx/flexitype/client"
)

// Logger is the service's structured logger.
type Logger = *slog.Logger

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("kitchen stopped", "error", err)
		os.Exit(1)
	}
}

func run(log Logger) error {
	addr := envOr("KITCHEN_ADDR", ":9400")
	flexitypeURL := envOr("FLEXITYPE_URL", "http://flexitype:8080")
	token := os.Getenv("FLEXITYPE_TOKEN")

	opts := []client.Option{client.WithUserAgent("kitchen")}
	if token != "" {
		opts = append(opts, client.WithToken(token))
	}
	c, err := client.New(flexitypeURL, opts...)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := waitForFlexitype(ctx, c, 60*time.Second, log); err != nil {
		return err
	}
	if err := ensureSchema(ctx, c, log); err != nil {
		return err
	}

	api, err := NewAPI(ctx, c, log)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: addr, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("kitchen listening", "addr", addr, "flexitype", flexitypeURL)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("kitchen stopped cleanly")
	return nil
}

// waitForFlexitype blocks until the service answers. Compose starts everything
// at once, so the first call normally loses the race.
func waitForFlexitype(ctx context.Context, c *client.Client, timeout time.Duration, log Logger) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := c.Types().List(ctx, client.ListTypesOptions{ListOptions: client.ListOptions{Limit: 1}}); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("flexitype did not become reachable in time")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			log.Info("waiting for flexitype")
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
