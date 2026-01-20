// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package events

import "errors"

// Watcher errors.
var (
	// ErrNodeNameRequired is returned when NodeName is not set in config.
	ErrNodeNameRequired = errors.New("node name is required")

	// ErrAlreadyStarted is returned when Start is called on a running watcher.
	ErrAlreadyStarted = errors.New("watcher already started")
)

// ProcessMapper errors.
var (
	// ErrInvalidPID is returned when an invalid PID is provided.
	ErrInvalidPID = errors.New("invalid PID")
)
