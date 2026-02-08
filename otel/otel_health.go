package otel

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// HealthServer provides /healthz and /readyz endpoints on localhost.
type HealthServer struct {
	port   int
	logger *slog.Logger
	server *http.Server
	ready  bool
}

// NewHealthServer creates a new health server.
func NewHealthServer(port int, logger *slog.Logger) *HealthServer {
	return &HealthServer{
		port:   port,
		logger: logger,
		ready:  true,
	}
}

// Start begins serving health endpoints. Call from a goroutine.
func (h *HealthServer) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if h.ready {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "ready")
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "not ready")
		}
	})

	h.server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", h.port),
		Handler: mux,
	}

	listener, err := net.Listen("tcp", h.server.Addr)
	if err != nil {
		h.logger.Warn("health server failed to start", "error", err)
		return
	}

	if err := h.server.Serve(listener); err != nil && err != http.ErrServerClosed {
		h.logger.Warn("health server error", "error", err)
	}
}

// Stop gracefully shuts down the health server.
func (h *HealthServer) Stop() {
	if h.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.server.Shutdown(ctx)
	}
}

// SetReady sets the readiness state.
func (h *HealthServer) SetReady(ready bool) {
	h.ready = ready
}
