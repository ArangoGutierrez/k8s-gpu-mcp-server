// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

// Package blackbox implements a Flight Recorder for GPU workloads.
//
// The Flight Recorder continuously captures GPU telemetry into in-memory
// ring buffers, providing historical context for failure analysis. Key
// features:
//
//   - Continuous sampling at configurable intervals (default: 10s)
//   - Bounded memory usage via fixed-size ring buffers
//   - Thread-safe operations for concurrent access
//   - Query methods for historical data retrieval
//
// # Architecture
//
// The Recorder coordinates sampling across all GPUs, storing snapshots
// in per-GPU ring buffers. Each buffer holds ~30 minutes of history.
//
//	                 Recorder
//	                    │
//	     ┌──────────────┼──────────────┐
//	     ▼              ▼              ▼
//	RingBuffer     RingBuffer     RingBuffer
//	(GPU 0)        (GPU 1)        (GPU N)
//
// # Usage
//
//	recorder := blackbox.NewRecorder(nvmlClient, blackbox.DefaultConfig())
//	if err := recorder.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	defer recorder.Stop()
//
//	// Query recent history
//	timeline, _ := recorder.GetTimeline("GPU-UUID", 5*time.Minute)
//
// # Memory Budget
//
// With default settings (10s interval, 30min retention, 8 GPUs):
// ~1.7MB total, well under the 2MB budget.
package blackbox
