// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

// Package main is the entry point for the k8s-gpu-mcp-server MCP server.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/internal/info"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/k8s"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/mcp"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/nvml"
	"k8s.io/klog/v2"
)

// ValidLogLevels are the accepted log levels.
var ValidLogLevels = []string{"debug", "info", "warn", "error"}

// resolveLogLevel determines the effective log level from env var and flag.
// Priority: LOG_LEVEL env var > --log-level flag > default ("info")
func resolveLogLevel(flagValue string) string {
	// Check environment variable first
	if envLevel := os.Getenv("LOG_LEVEL"); envLevel != "" {
		level := strings.ToLower(strings.TrimSpace(envLevel))
		if isValidLogLevel(level) {
			return level
		}
		// Invalid env value - log warning and fall back to flag
		klog.V(2).InfoS("invalid LOG_LEVEL env var",
			"value", envLevel, "valid", ValidLogLevels, "using", flagValue)
	}
	return flagValue
}

// isValidLogLevel checks if a log level is valid.
func isValidLogLevel(level string) bool {
	for _, valid := range ValidLogLevels {
		if level == valid {
			return true
		}
	}
	return false
}

const (
	// ModeReadOnly enables only read-only operations (default)
	ModeReadOnly = "read-only"
	// ModeOperator enables write operations (kill/reset)
	ModeOperator = "operator"
)

func main() {
	// Initialize klog flags (adds -v, -logtostderr, etc.)
	klog.InitFlags(nil)

	// Parse command-line flags
	var (
		mode     = flag.String("mode", ModeReadOnly, "Operation mode: read-only or operator")
		nvmlMode = flag.String("nvml-mode", "mock", "NVML mode: mock or real (requires GPU hardware)")
		showVer  = flag.Bool("version", false, "Show version information and exit")
		logLevel = flag.String("log-level", "info", "Log level: debug, info, warn, error")

		// HTTP transport flags
		port = flag.Int("port", 0, "HTTP port (0 = stdio mode, >0 = HTTP mode)")
		addr = flag.String("addr", "0.0.0.0", "HTTP listen address")

		// Gateway mode flags
		gatewayMode = flag.Bool("gateway", false,
			"Enable gateway mode (routes to node agents via K8s pod exec)")
		namespace = flag.String("namespace", "gpu-diagnostics",
			"Namespace for GPU agent pods (gateway mode)")
		routingMode = flag.String("routing-mode", "http",
			"Gateway routing mode: http (default, direct HTTP) or exec (legacy)")

		// Oneshot mode for exec-based invocations
		oneshot = flag.Int("oneshot", 0,
			"Exit after processing N requests (0=disabled, 2=init+tool)")

		// TLS flags
		tlsCertFile = flag.String("tls-cert-file", "",
			"Path to TLS certificate PEM file (enables HTTPS when set)")
		tlsKeyFile = flag.String("tls-key-file", "",
			"Path to TLS private key PEM file (required with --tls-cert-file)")
	)
	flag.Parse()

	// Flush logs on exit
	defer klog.Flush()

	// Show version and exit if requested
	if *showVer {
		buildInfo := info.GetInfo()
		fmt.Fprintf(os.Stderr, "k8s-gpu-mcp-server version %s (commit %s)\n",
			buildInfo.Version, buildInfo.GitCommit)
		os.Exit(0)
	}

	// Validate mode flag
	if *mode != ModeReadOnly && *mode != ModeOperator {
		klog.ErrorS(nil, "invalid mode",
			"mode", *mode, "valid", []string{"read-only", "operator"})
		klog.Flush()
		os.Exit(1)
	}

	// Validate nvml-mode flag (only relevant in non-gateway mode)
	if !*gatewayMode && *nvmlMode != "mock" && *nvmlMode != "real" {
		klog.ErrorS(nil, "invalid nvml-mode",
			"nvmlMode", *nvmlMode, "valid", []string{"mock", "real"})
		klog.Flush()
		os.Exit(1)
	}

	// Resolve log level from env var and flag
	effectiveLogLevel := resolveLogLevel(*logLevel)
	if !isValidLogLevel(effectiveLogLevel) {
		klog.ErrorS(nil, "invalid log-level",
			"logLevel", effectiveLogLevel, "valid", ValidLogLevels)
		klog.Flush()
		os.Exit(1)
	}

	// Validate routing mode if in gateway mode (fail fast before logging)
	if *gatewayMode {
		if *routingMode != "http" && *routingMode != "exec" {
			klog.ErrorS(nil, "invalid routing-mode",
				"routingMode", *routingMode, "valid", []string{"http", "exec"})
			klog.Flush()
			os.Exit(1)
		}
	}

	// Validate and configure transport mode
	var transport mcp.TransportType
	var httpAddr string

	if *port > 0 {
		if *port < 1 || *port > 65535 {
			klog.ErrorS(nil, "invalid port",
				"port", *port, "valid", "1-65535 or 0 for stdio")
			klog.Flush()
			os.Exit(1)
		}
		transport = mcp.TransportHTTP
		httpAddr = fmt.Sprintf("%s:%d", *addr, *port)
		klog.InfoS("HTTP mode enabled", "addr", httpAddr)
	} else {
		transport = mcp.TransportStdio
	}

	// Resolve TLS configuration from flags and env vars.
	// Environment variables take precedence over flags.
	tlsCfg := &mcp.TLSConfig{
		CertFile: *tlsCertFile,
		KeyFile:  *tlsKeyFile,
	}
	if envCert := os.Getenv("TLS_CERT_FILE"); envCert != "" {
		tlsCfg.CertFile = envCert
	}
	if envKey := os.Getenv("TLS_KEY_FILE"); envKey != "" {
		tlsCfg.KeyFile = envKey
	}
	if err := tlsCfg.Validate(); err != nil {
		klog.ErrorS(err, "invalid TLS configuration")
		klog.Flush()
		os.Exit(1)
	}
	if tlsCfg.Enabled() {
		klog.InfoS("TLS enabled", "certFile", tlsCfg.CertFile, "keyFile", tlsCfg.KeyFile)
	}

	// Log startup information to stderr (structured)
	if *gatewayMode {
		klog.InfoS("starting k8s-gpu-mcp-server",
			"version", info.Version(),
			"commit", info.GitCommit(),
			"mode", *mode,
			"gateway", true,
			"namespace", *namespace,
			"routingMode", *routingMode,
			"logLevel", effectiveLogLevel,
			"k8sNode", os.Getenv("NODE_NAME"),
			"k8sPod", os.Getenv("POD_NAME"),
			"k8sNamespace", os.Getenv("POD_NAMESPACE"))
	} else {
		klog.InfoS("starting k8s-gpu-mcp-server",
			"version", info.Version(),
			"commit", info.GitCommit(),
			"mode", *mode,
			"nvmlMode", *nvmlMode,
			"logLevel", effectiveLogLevel,
			"k8sNode", os.Getenv("NODE_NAME"),
			"k8sPod", os.Getenv("POD_NAME"),
			"k8sNamespace", os.Getenv("POD_NAMESPACE"))
	}

	// Setup context with cancellation for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Channel to coordinate shutdown
	done := make(chan error, 1)

	// Build MCP server config
	buildInfo := info.GetInfo()
	mcpCfg := mcp.Config{
		Mode:        *mode,
		Version:     buildInfo.Version,
		GitCommit:   buildInfo.GitCommit,
		Transport:   transport,
		HTTPAddr:    httpAddr,
		GatewayMode: *gatewayMode,
		Namespace:   *namespace,
		Oneshot:     *oneshot,
		RoutingMode: *routingMode,
		TLS:         tlsCfg,
	}

	if *gatewayMode {
		// Gateway mode: initialize K8s client
		klog.InfoS("initializing K8s client", "namespace", *namespace)

		k8sClient, err := k8s.NewClient(*namespace)
		if err != nil {
			klog.ErrorS(err, "failed to create K8s client")
			klog.Flush()
			os.Exit(1)
		}
		mcpCfg.K8sClient = k8sClient
	} else {
		// Regular mode: initialize NVML client
		var nvmlClient nvml.Interface
		if *nvmlMode == "real" {
			klog.InfoS("initializing real NVML (requires GPU hardware)")
			nvmlClient = nvml.NewReal()
		} else {
			klog.InfoS("initializing mock NVML", "fakeGPUs", 2)
			nvmlClient = nvml.NewMock(2)
		}

		if err := nvmlClient.Init(ctx); err != nil {
			klog.ErrorS(err, "failed to initialize NVML", "nvmlMode", *nvmlMode)
			klog.Flush()
			os.Exit(1)
		}
		defer func() {
			if err := nvmlClient.Shutdown(ctx); err != nil {
				klog.ErrorS(err, "failed to shutdown NVML")
			}
		}()
		mcpCfg.NVMLClient = nvmlClient
	}

	// Initialize MCP server
	mcpServer, err := mcp.New(mcpCfg)
	if err != nil {
		klog.ErrorS(err, "failed to create MCP server")
		klog.Flush()
		os.Exit(1)
	}

	// Start the MCP server in a goroutine
	go func() {
		if err := mcpServer.Run(ctx); err != nil {
			klog.ErrorS(err, "MCP server error")
			done <- err
			return
		}
		done <- nil
	}()

	// Wait for shutdown signal or server completion
	serverCompleted := false
	select {
	case sig := <-sigCh:
		klog.InfoS("received signal", "signal", sig.String())
		cancel()
	case err := <-done:
		serverCompleted = true
		if err != nil {
			klog.ErrorS(err, "server error")
			klog.Flush()
			os.Exit(1)
		}
	}

	// Wait for graceful shutdown only if interrupted (not if server completed
	// normally). In oneshot mode, the server sends to done exactly once and
	// exits - no second wait needed.
	if !serverCompleted {
		<-done
	}
	klog.InfoS("shutdown complete")
}
