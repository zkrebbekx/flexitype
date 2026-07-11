// Package shutdown coordinates graceful teardown: tasks register with a
// priority and run highest-first when SIGINT/SIGTERM arrives, each bounded
// by the shutdown timeout.
package shutdown

import (
	"context"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/zkrebbekx/flexitype/pkg/logger"
)

// Task is one teardown step. Higher priorities run first: drain servers
// (~90), flush telemetry (~50), close the database (~10).
type Task struct {
	Name     string
	Priority int
	Handler  func(ctx context.Context) error
}

// Config controls the handler.
type Config struct {
	Logger  *logger.Logger
	Timeout time.Duration
}

// Handler owns the registered teardown tasks.
type Handler struct {
	cfg   Config
	mu    sync.Mutex
	tasks []Task
}

// New builds a shutdown handler.
func New(cfg Config) *Handler {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &Handler{cfg: cfg}
}

// RegisterTask adds a teardown step.
func (h *Handler) RegisterTask(t Task) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tasks = append(h.tasks, t)
}

// Wait blocks until SIGINT/SIGTERM (or ctx cancellation), then runs every
// task in priority order. It returns once teardown completes.
func (h *Handler) Wait(ctx context.Context) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)

	select {
	case s := <-sig:
		if h.cfg.Logger != nil {
			h.cfg.Logger.Info().Str("signal", s.String()).Msg("shutdown signal received")
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), h.cfg.Timeout)
	defer cancel()

	h.mu.Lock()
	tasks := make([]Task, len(h.tasks))
	copy(tasks, h.tasks)
	h.mu.Unlock()

	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].Priority > tasks[j].Priority })

	for _, t := range tasks {
		if err := t.Handler(shutdownCtx); err != nil && h.cfg.Logger != nil {
			h.cfg.Logger.Error().Err(err).Str("task", t.Name).Msg("shutdown task failed")
		} else if h.cfg.Logger != nil {
			h.cfg.Logger.Info().Str("task", t.Name).Msg("shutdown task complete")
		}
	}
}
