// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

// Package dcgm provides an optional abstraction layer over NVIDIA DCGM
// (Data Center GPU Manager).
//
// # Overview
//
// This package enables advanced GPU telemetry features when DCGM is available,
// while gracefully degrading to NVML-only mode when it is not. DCGM provides
// capabilities beyond NVML including:
//
//   - Native XID error field (no log parsing required)
//   - GPU profiling metrics (SM occupancy, tensor core utilization)
//   - NVSwitch fabric monitoring (multi-node GPU communication)
//   - Built-in time-series with statistical aggregation
//   - Health policies and configurable thresholds
//
// # Modes
//
// The package supports two connection modes:
//
//   - Embedded: Agent starts nv-hostengine internally (self-contained)
//   - External: Agent connects to existing DCGM daemon via socket
//
// # Requirements
//
// DCGM requires datacenter-class GPUs (Tesla, A100, H100, etc.).
// Consumer GPUs should continue using NVML-only mode.
//
// # Usage
//
// The interface follows the same patterns as pkg/nvml:
//
//	// Create DCGM client (embedded mode)
//	dcgmClient := dcgm.NewEmbedded(dcgm.DefaultConfig(), logger)
//
//	// Initialize
//	if err := dcgmClient.Init(ctx); err != nil {
//	    // Fall back to NVML-only
//	    dcgmClient = dcgm.NewStub()
//	}
//	defer dcgmClient.Shutdown(ctx)
//
//	// Check availability
//	if dcgmClient.IsAvailable() {
//	    metrics, _ := dcgmClient.GetProfilingMetrics(0)
//	    fmt.Printf("SM Occupancy: %.1f%%\n", metrics.SMOccupancy)
//	}
//
// # Agent Integration
//
// To integrate DCGM into cmd/agent, add the following to the agent setup:
//
//	// In cmd/agent/main.go or similar
//	var dcgmClient dcgm.Interface
//
//	if cfg.DCGM.Enabled {
//	    switch cfg.DCGM.Mode {
//	    case "embedded":
//	        dcgmClient = dcgm.NewEmbedded(cfg.DCGM, logger)
//	    case "external":
//	        dcgmClient = dcgm.NewClient(cfg.DCGM.Socket, logger)
//	    }
//	    if err := dcgmClient.Init(ctx); err != nil {
//	        logger.Warn("DCGM unavailable, using NVML-only", "error", err)
//	        dcgmClient = dcgm.NewStub()
//	    }
//	} else {
//	    dcgmClient = dcgm.NewStub()
//	}
//	defer dcgmClient.Shutdown(ctx)
//
// # Current Status
//
// This package currently provides placeholder implementations. The Client type
// returns simulated/empty data for most operations. The actual DCGM bindings
// will be implemented in a future PR.
//
// # Build Tags
//
// The placeholder client (client.go) uses a build constraint:
//
//	//go:build !dcgm_cgo
//
// When real DCGM CGO bindings are added, build with -tags dcgm_cgo to use them:
//
//	go build -tags dcgm_cgo ./...
//
// Default builds (no tags) use the placeholder implementation.
//
// # Runtime Detection
//
// Use IsPlaceholder() to detect placeholder vs real bindings at runtime:
//
//	client := dcgm.NewClient(endpoint, logger)
//	if client.IsPlaceholder() {
//	    logger.Warn("using placeholder DCGM client, profiling unavailable")
//	}
//
// # Future Work (Issue #163)
//
// The following enhancements are planned:
//
//  1. CGO Bindings: Replace placeholder Client with actual DCGM CGO bindings.
//     Reference: https://github.com/NVIDIA/go-dcgm
//     Build tag "dcgm_cgo" is reserved for this implementation.
//
//  2. Background Polling: WatchFields should start a goroutine that polls
//     DCGM at the specified interval and caches values.
//
//  3. Reconnection Logic: Implement Reconnect() with exponential backoff.
//     The interface method is already defined; implementations currently
//     return no-op or ErrNotInitialized.
//
//  4. Metrics Export: Add Prometheus metrics for DCGM connection state
//     and query latency.
//
//  5. Tool Integration: Update analyze_xid tool to prefer DCGM's native
//     XID field over /dev/kmsg parsing when available.
package dcgm
