// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPServer_Healthz(t *testing.T) {
	mcpServer := server.NewMCPServer("test", "1.0.0")
	httpServer := NewHTTPServer(mcpServer, ":0", "1.0.0")

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	httpServer.handleHealthz(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "healthy", resp["status"])
}

func TestHTTPServer_Readyz(t *testing.T) {
	mcpServer := server.NewMCPServer("test", "1.0.0")
	httpServer := NewHTTPServer(mcpServer, ":0", "1.0.0")

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	httpServer.handleReadyz(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "ready", resp["status"])
}

func TestHTTPServer_Version(t *testing.T) {
	mcpServer := server.NewMCPServer("test", "1.0.0")
	httpServer := NewHTTPServer(mcpServer, ":0", "1.2.3")

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	w := httptest.NewRecorder()

	httpServer.handleVersion(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "k8s-gpu-mcp-server", resp["server"])
	assert.Equal(t, "1.2.3", resp["version"])
}

func TestHTTPServer_Shutdown(t *testing.T) {
	mcpServer := server.NewMCPServer("test", "1.0.0")
	httpServer := NewHTTPServer(mcpServer, ":0", "1.0.0")

	// Shutdown without starting should not error
	err := httpServer.Shutdown()
	assert.NoError(t, err)
}

func TestHTTPServer_ListenAndServe_Shutdown(t *testing.T) {
	mcpServer := server.NewMCPServer("test", "1.0.0")
	// Use port 0 to get a random available port
	httpServer := NewHTTPServer(mcpServer, "127.0.0.1:0", "1.0.0")

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe(ctx)
	}()

	// Wait for server to be ready using the Ready() method
	select {
	case <-httpServer.Ready():
		// Server is ready
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start in time")
	}

	// Cancel context to trigger shutdown
	cancel()

	// Wait for server to stop
	select {
	case err := <-errCh:
		// Shutdown error is acceptable
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}

func TestHTTPServer_BindFailure_NoReadySignal(t *testing.T) {
	mcpServer := server.NewMCPServer("test", "1.0.0")

	// Create a listener first to occupy a port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	// Get the actual address the listener is using
	actualAddr := ln.Addr().String()
	t.Logf("blocking port: %s", actualAddr)

	// Try to start server on the same port - should fail immediately
	httpServer := NewHTTPServer(mcpServer, actualAddr, "1.0.0")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe(ctx)
	}()

	// The server should fail immediately with bind error
	// and the ready channel should NOT be closed
	select {
	case <-httpServer.Ready():
		t.Fatal("ready channel was closed despite bind failure")
	case err := <-errCh:
		// Expected: bind error
		require.Error(t, err)
		assert.Contains(t, err.Error(), "address already in use")
	case <-time.After(2 * time.Second):
		t.Fatal("expected immediate bind failure")
	}
}

func TestNewHTTPServer(t *testing.T) {
	mcpServer := server.NewMCPServer("test", "1.0.0")
	httpServer := NewHTTPServer(mcpServer, "0.0.0.0:8080", "2.0.0")

	assert.NotNil(t, httpServer)
	assert.Equal(t, "0.0.0.0:8080", httpServer.addr)
	assert.Equal(t, "2.0.0", httpServer.version)
	assert.Equal(t, mcpServer, httpServer.mcpServer)
	assert.Nil(t, httpServer.httpServer) // Not started yet
}

func TestTransportType_Constants(t *testing.T) {
	assert.Equal(t, TransportType("stdio"), TransportStdio)
	assert.Equal(t, TransportType("http"), TransportHTTP)
}

func TestRequireBearerAuth(t *testing.T) {
	mcpServer := server.NewMCPServer("test", "1.0.0")
	httpServer := NewHTTPServer(mcpServer, ":0", "1.0.0")
	httpServer.SetMetricsAuthToken("secret-token-123")

	// Wrap a simple OK handler with the auth middleware
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	protected := httpServer.requireBearerAuth(okHandler)

	t.Run("no_auth_header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		w := httptest.NewRecorder()
		protected.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "authorization required")
	})

	t.Run("wrong_token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		w := httptest.NewRecorder()
		protected.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "invalid token")
	})

	t.Run("invalid_scheme", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		w := httptest.NewRecorder()
		protected.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "invalid authorization scheme")
	})

	t.Run("valid_token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.Header.Set("Authorization", "Bearer secret-token-123")
		w := httptest.NewRecorder()
		protected.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestHTTPServer_MetricsNoAuth_WhenTokenEmpty(t *testing.T) {
	// When no auth token is configured, metrics should be accessible
	mcpServer := server.NewMCPServer("test", "1.0.0")
	httpServer := NewHTTPServer(mcpServer, ":0", "1.0.0")
	// metricsAuthToken is empty by default — no auth required
	assert.Empty(t, httpServer.metricsAuthToken)
}

func TestHTTPServer_MethodNotAllowed(t *testing.T) {
	mcpServer := server.NewMCPServer("test", "1.0.0")
	httpServer := NewHTTPServer(mcpServer, ":0", "1.0.0")

	tests := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		path    string
	}{
		{"healthz", httpServer.handleHealthz, "/healthz"},
		{"readyz", httpServer.handleReadyz, "/readyz"},
		{"version", httpServer.handleVersion, "/version"},
	}

	for _, tt := range tests {
		t.Run(tt.name+"_POST", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			w := httptest.NewRecorder()
			tt.handler(w, req)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})

		t.Run(tt.name+"_PUT", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, tt.path, nil)
			w := httptest.NewRecorder()
			tt.handler(w, req)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})
	}
}
