// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

// Package mcp provides the MCP server implementation for stdio and HTTP
// transports.
package mcp

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/blackbox"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/gateway"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/k8s"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/nvml"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/prompts"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/tools"
	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/xid"
	"github.com/mark3labs/mcp-go/server"
	"k8s.io/klog/v2"
)

// TransportType defines the transport mode for the MCP server.
type TransportType string

const (
	// TransportStdio uses stdin/stdout for communication (default).
	TransportStdio TransportType = "stdio"
	// TransportHTTP uses HTTP/SSE for communication.
	TransportHTTP TransportType = "http"
)

// Server wraps the MCP server with configurable transport.
type Server struct {
	mcpServer   *server.MCPServer
	mode        string
	nvmlClient  nvml.Interface
	transport   TransportType
	httpAddr    string
	version     string
	gatewayMode bool
	k8sClient   *k8s.Client
	oneshot     int
	wg          sync.WaitGroup // goroutine lifecycle coordination
}

// Config holds server configuration.
type Config struct {
	// Mode is the operation mode: "read-only" or "operator"
	Mode string
	// Version is the agent version
	Version string
	// GitCommit is the git commit hash
	GitCommit string
	// NVMLClient is the NVML interface implementation (nil in gateway mode)
	NVMLClient nvml.Interface
	// Transport is the transport mode: "stdio" or "http"
	Transport TransportType
	// HTTPAddr is the HTTP listen address (e.g., "0.0.0.0:8080")
	HTTPAddr string
	// GatewayMode enables routing to node agents via K8s pod exec
	GatewayMode bool
	// Namespace for GPU agent pods (gateway mode only)
	Namespace string
	// K8sClient is the Kubernetes client (gateway mode only)
	K8sClient *k8s.Client
	// Oneshot exits after processing N requests (0=disabled)
	Oneshot int
	// RoutingMode specifies gateway routing: "http" (default) or "exec"
	RoutingMode string
	// Recorder is the flight recorder for GPU telemetry (optional)
	Recorder *blackbox.Recorder
	// XIDWatcher is the XID event watcher (optional)
	XIDWatcher *xid.Watcher
}

// New creates a new MCP server instance.
func New(cfg Config) (*Server, error) {
	if cfg.Mode == "" {
		cfg.Mode = "read-only"
	}

	// Gateway mode requires K8s client, regular mode requires NVML client
	if cfg.GatewayMode {
		if cfg.K8sClient == nil {
			return nil, fmt.Errorf("K8sClient is required for gateway mode")
		}
	} else {
		if cfg.NVMLClient == nil {
			return nil, fmt.Errorf("NVMLClient is required")
		}
	}

	// Default to stdio transport
	if cfg.Transport == "" {
		cfg.Transport = TransportStdio
	}

	// Validate HTTPAddr is set when using HTTP transport
	if cfg.Transport == TransportHTTP && cfg.HTTPAddr == "" {
		return nil, fmt.Errorf("HTTPAddr is required for HTTP transport")
	}

	s := &Server{
		mode:        cfg.Mode,
		nvmlClient:  cfg.NVMLClient,
		transport:   cfg.Transport,
		httpAddr:    cfg.HTTPAddr,
		version:     cfg.Version,
		gatewayMode: cfg.GatewayMode,
		k8sClient:   cfg.K8sClient,
		oneshot:     cfg.Oneshot,
	}

	// Create MCP server with prompt capabilities
	mcpServer := server.NewMCPServer(
		"k8s-gpu-mcp-server",
		cfg.Version,
		server.WithPromptCapabilities(true),
	)

	if cfg.GatewayMode {
		// Gateway mode: register GPU tools with proxy handlers
		// Note: list_gpu_nodes was consolidated into get_gpu_inventory
		// which now returns cluster summary with node info

		// Determine routing mode option
		var routerOpts []gateway.RouterOption
		if cfg.RoutingMode == "exec" {
			routerOpts = append(routerOpts,
				gateway.WithRoutingMode(gateway.RoutingModeExec))
		} else {
			// Default to HTTP
			routerOpts = append(routerOpts,
				gateway.WithRoutingMode(gateway.RoutingModeHTTP))
		}

		inventoryProxy := gateway.NewProxyHandler(cfg.K8sClient,
			"get_gpu_inventory", routerOpts...)
		mcpServer.AddTool(tools.GetGPUInventoryTool(), inventoryProxy.Handle)

		healthProxy := gateway.NewProxyHandler(cfg.K8sClient,
			"get_gpu_health", routerOpts...)
		mcpServer.AddTool(tools.GetGPUHealthTool(), healthProxy.Handle)

		xidProxy := gateway.NewProxyHandler(cfg.K8sClient,
			"analyze_xid_errors", routerOpts...)
		mcpServer.AddTool(tools.GetAnalyzeXIDTool(), xidProxy.Handle)

		nvlinkProxy := gateway.NewProxyHandler(cfg.K8sClient,
			"get_nvlink_topology", routerOpts...)
		mcpServer.AddTool(tools.GetNVLinkTopologyTool(), nvlinkProxy.Handle)

		// Register K8s-native tools (don't need proxy, query K8s API directly)
		podGPUHandler := tools.NewPodGPUAllocationHandler(
			cfg.K8sClient.Clientset())
		mcpServer.AddTool(tools.GetPodGPUAllocationTool(), podGPUHandler.Handle)

		// describe_gpu_node needs both K8s and NVML (nil in gateway mode)
		// It will use K8s capacity data when NVML is unavailable
		describeHandler := tools.NewDescribeGPUNodeHandler(
			cfg.K8sClient.Clientset(), nil)
		mcpServer.AddTool(tools.GetDescribeGPUNodeTool(), describeHandler.Handle)

		// explain_failure - flagship GPU failure analysis tool
		// Gateway-only: requires K8s API access to query pod status.
		// In gateway mode, correlator/recorder are nil, so handler
		// falls back to minimal analysis based on K8s data only.
		explainHandler, err := tools.NewExplainFailureHandler(
			cfg.K8sClient.Clientset(),
		)
		if err != nil {
			return nil, fmt.Errorf("create explain_failure handler: %w", err)
		}
		mcpServer.AddTool(tools.GetExplainFailureTool(), explainHandler.Handle)

		// get_incident_report - detailed incident report tool
		// Gateway-only: requires K8s API access to query pod status.
		// Provides comprehensive incident data for deep debugging and post-mortems.
		reportHandler, err := tools.NewGetIncidentReportHandler(
			cfg.K8sClient.Clientset(),
		)
		if err != nil {
			return nil, fmt.Errorf("create get_incident_report handler: %w", err)
		}
		mcpServer.AddTool(tools.GetIncidentReportTool(), reportHandler.Handle)

		// get_gpu_timeline - historical GPU metrics from flight recorder
		// In gateway mode, proxy requests to node agents where recorder runs.
		timelineProxy := gateway.NewProxyHandler(cfg.K8sClient,
			"get_gpu_timeline", routerOpts...)
		mcpServer.AddTool(tools.GetGPUTimelineTool(), timelineProxy.Handle)

		// Register prompts
		registerPrompts(mcpServer)

		klog.InfoS("MCP server initialized",
			"mode", cfg.Mode,
			"gateway", true,
			"namespace", cfg.Namespace,
			"routingMode", cfg.RoutingMode,
			"tools", []string{"get_gpu_inventory", "get_gpu_health",
				"analyze_xid_errors", "get_nvlink_topology",
				"get_pod_gpu_allocation", "describe_gpu_node",
				"explain_failure", "get_incident_report", "get_gpu_timeline"},
			"prompts", prompts.GetAllPromptNames(),
			"version", cfg.Version,
			"commit", cfg.GitCommit)
	} else {
		// Regular mode: register GPU tools with NVML
		gpuInventoryHandler := tools.NewGPUInventoryHandler(cfg.NVMLClient)
		mcpServer.AddTool(tools.GetGPUInventoryTool(),
			gpuInventoryHandler.Handle)

		xidHandler := tools.NewAnalyzeXIDHandler(cfg.NVMLClient)
		mcpServer.AddTool(tools.GetAnalyzeXIDTool(), xidHandler.Handle)

		healthHandler := tools.NewGPUHealthHandler(cfg.NVMLClient)
		mcpServer.AddTool(tools.GetGPUHealthTool(), healthHandler.Handle)

		nvlinkHandler := tools.NewNVLinkTopologyHandler(cfg.NVMLClient)
		mcpServer.AddTool(tools.GetNVLinkTopologyTool(), nvlinkHandler.Handle)

		// get_gpu_timeline - historical GPU metrics from flight recorder
		// Only register if recorder is available (started at main level).
		toolsList := []string{"get_gpu_inventory", "get_gpu_health",
			"analyze_xid_errors", "get_nvlink_topology"}
		if cfg.Recorder != nil {
			var timelineOpts []tools.GPUTimelineOption
			timelineOpts = append(timelineOpts,
				tools.WithTimelineRecorder(cfg.Recorder))
			if cfg.XIDWatcher != nil {
				timelineOpts = append(timelineOpts,
					tools.WithTimelineXIDWatcher(cfg.XIDWatcher))
			}
			timelineHandler := tools.NewGPUTimelineHandler(
				cfg.NVMLClient, timelineOpts...)
			mcpServer.AddTool(tools.GetGPUTimelineTool(), timelineHandler.Handle)
			toolsList = append(toolsList, "get_gpu_timeline")
		}

		// Register prompts
		registerPrompts(mcpServer)

		klog.InfoS("MCP server initialized",
			"mode", cfg.Mode,
			"gateway", false,
			"tools", toolsList,
			"prompts", prompts.GetAllPromptNames(),
			"version", cfg.Version,
			"commit", cfg.GitCommit)
	}

	s.mcpServer = mcpServer

	return s, nil
}

// Run starts the MCP server with the configured transport.
func (s *Server) Run(ctx context.Context) error {
	switch s.transport {
	case TransportHTTP:
		return s.runHTTP(ctx)
	default:
		return s.runStdio(ctx)
	}
}

// runStdio runs the server with stdio transport.
//
// Graceful shutdown: When the context is cancelled, we close os.Stdin to
// unblock the mcp-go ServeStdio call, allowing it to return gracefully.
// This is the only reliable way to interrupt blocking stdio reads.
func (s *Server) runStdio(ctx context.Context) error {
	klog.InfoS("MCP server starting", "transport", "stdio", "mode", s.mode)

	// Oneshot mode: use OneshotTransport for deterministic exit
	if s.oneshot > 0 {
		transport, err := NewOneshotTransport(OneshotConfig{
			MCPServer:   s.mcpServer,
			Reader:      os.Stdin,
			Writer:      os.Stdout,
			MaxRequests: s.oneshot,
		})
		if err != nil {
			return fmt.Errorf("failed to create oneshot transport: %w", err)
		}

		result, err := transport.Run(ctx)
		if err != nil {
			return err
		}

		klog.InfoS("oneshot completed",
			"processed", result.Processed, "errors", result.Errors)
		return nil
	}

	// Standard mode: run server with stdio transport in a goroutine
	errCh := make(chan error, 1)
	done := make(chan struct{})

	s.wg.Add(1) // Track goroutine in WaitGroup
	go func() {
		defer s.wg.Done() // Signal completion
		defer close(done)
		if err := server.ServeStdio(s.mcpServer); err != nil {
			errCh <- fmt.Errorf("MCP server error: %w", err)
		}
	}()

	// Wait for context cancellation or server error
	select {
	case <-ctx.Done():
		klog.InfoS("MCP server stopping", "reason", "context cancelled")

		// Close stdin to unblock ServeStdio
		// This is safe: os.Stdin.Close() is idempotent
		if err := os.Stdin.Close(); err != nil {
			klog.V(2).InfoS("failed to close stdin", "error", err)
		}

		// Wait for ServeStdio to return (with timeout)
		select {
		case <-done:
			klog.V(2).InfoS("stdio server stopped gracefully")
		case err := <-errCh:
			// ServeStdio may return an error after stdin close
			klog.V(2).InfoS("stdio server returned", "error", err)
		}

		return s.Shutdown()

	case err := <-errCh:
		klog.ErrorS(err, "MCP server error")
		return err
	}
}

// runHTTP runs the server with HTTP transport.
func (s *Server) runHTTP(ctx context.Context) error {
	klog.InfoS("MCP server starting",
		"transport", "http", "addr", s.httpAddr, "mode", s.mode)

	httpServer := NewHTTPServer(s.mcpServer, s.httpAddr, s.version)
	return httpServer.ListenAndServe(ctx)
}

// registerPrompts registers all prompts from the library with the MCP server.
func registerPrompts(mcpServer *server.MCPServer) {
	for _, promptDef := range prompts.Library {
		p := promptDef.ToMCPPrompt()
		handler := promptDef.BuildHandler()
		mcpServer.AddPrompt(p, handler)
	}
}

// Shutdown gracefully shuts down the MCP server.
func (s *Server) Shutdown() error {
	klog.InfoS("MCP server shutdown initiated")

	// Wait for all tracked goroutines to complete.
	// Note: The mcp-go library doesn't expose a shutdown method,
	// so we rely on context cancellation and stdin closure in runStdio
	// to signal the server to stop.
	s.wg.Wait()

	klog.InfoS("MCP server shutdown complete")
	return nil
}
