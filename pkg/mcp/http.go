// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/klog/v2"
)

// NVMLChecker is an optional interface to check NVML initialization status.
type NVMLChecker interface {
	// IsInitialized returns true if NVML has been successfully initialized.
	IsInitialized() bool
}

// HTTPServer wraps the MCP server with HTTP transport.
type HTTPServer struct {
	mcpServer   *server.MCPServer
	httpServer  *http.Server
	addr        string
	version     string
	ready       chan struct{}
	nvmlChecker NVMLChecker
}

// NewHTTPServer creates an HTTP transport server.
func NewHTTPServer(mcpServer *server.MCPServer, addr, version string) *HTTPServer {
	return &HTTPServer{
		mcpServer: mcpServer,
		addr:      addr,
		version:   version,
		ready:     make(chan struct{}),
	}
}

// SetNVMLChecker sets the optional NVML checker for startup warnings.
func (h *HTTPServer) SetNVMLChecker(checker NVMLChecker) {
	h.nvmlChecker = checker
}

// ListenAndServe starts the HTTP server.
func (h *HTTPServer) ListenAndServe(ctx context.Context) error {
	// Warn if NVML is not initialized — tool calls will fail at runtime
	if h.nvmlChecker != nil && !h.nvmlChecker.IsInitialized() {
		klog.Warningf("NVML is not initialized; GPU tools will return errors until the driver is available")
	}

	mux := http.NewServeMux()

	// MCP endpoint - Streamable HTTP transport
	// Use stateless mode: each request is independent, no session tracking needed.
	// This allows the gateway to send tool calls directly without session management,
	// which is appropriate for in-cluster HTTP routing where each request is atomic.
	streamableServer := server.NewStreamableHTTPServer(
		h.mcpServer,
		server.WithStateLess(true),
	)
	mux.Handle("/mcp", streamableServer)

	// Health check endpoints
	mux.HandleFunc("/healthz", h.handleHealthz)
	mux.HandleFunc("/readyz", h.handleReadyz)

	// Version endpoint
	mux.HandleFunc("/version", h.handleVersion)

	// Prometheus metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// Create server before starting goroutine to avoid race condition.
	// WriteTimeout (90s) must exceed exec timeout (60s) plus response
	// marshaling buffer (30s buffer) to prevent race conditions causing
	// "socket hang up" errors.
	// IdleTimeout (120s) exceeds WriteTimeout to avoid prematurely closing
	// keep-alive connections during long-running operations.
	h.httpServer = &http.Server{
		Addr:              h.addr,
		Handler:           mux,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	klog.InfoS("HTTP server starting", "addr", h.addr)

	// Create listener first to verify the address is available.
	// This prevents the race condition where close(h.ready) is called
	// before we know if the server can actually bind to the address.
	ln, err := net.Listen("tcp", h.addr)
	if err != nil {
		return err
	}

	// Start server in goroutine using the pre-created listener
	errCh := make(chan error, 1)
	go func() {
		// Signal ready only after we've successfully created the listener
		close(h.ready)
		if err := h.httpServer.Serve(ln); err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		return h.Shutdown()
	case err := <-errCh:
		return err
	}
}

// Ready returns a channel that is closed when the server is ready to accept
// connections. This can be used to synchronize tests or health checks.
func (h *HTTPServer) Ready() <-chan struct{} {
	return h.ready
}

// Shutdown gracefully shuts down the HTTP server.
func (h *HTTPServer) Shutdown() error {
	if h.httpServer == nil {
		return nil
	}

	klog.InfoS("HTTP server shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return h.httpServer.Shutdown(ctx)
}

// handleHealthz handles liveness probe.
func (h *HTTPServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
	}); err != nil {
		klog.ErrorS(err, "failed to encode healthz response")
	}
}

// handleReadyz handles readiness probe.
func (h *HTTPServer) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	// Report degraded readiness if NVML is not initialized
	status := "ready"
	if h.nvmlChecker != nil && !h.nvmlChecker.IsInitialized() {
		klog.V(4).InfoS("readyz: NVML not initialized, reporting degraded")
		status = "degraded"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{
		"status": status,
	}); err != nil {
		klog.ErrorS(err, "failed to encode readyz response")
	}
}

// handleVersion returns version information.
func (h *HTTPServer) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{
		"server":  "k8s-gpu-mcp-server",
		"version": h.version,
	}); err != nil {
		klog.ErrorS(err, "failed to encode version response")
	}
}
