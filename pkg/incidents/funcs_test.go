// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package incidents

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatBytes_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    uint64
		expected string
	}{
		{name: "zero bytes", input: 0, expected: "0 bytes"},
		{name: "one byte", input: 1, expected: "1 bytes"},
		{name: "just under KB", input: 1023, expected: "1023 bytes"},
		{name: "exact KB boundary", input: 1024, expected: "1.0 KB"},
		{name: "just under MB", input: 1024*1024 - 1, expected: "1024.0 KB"},
		{name: "exact MB boundary", input: 1024 * 1024, expected: "1.0 MB"},
		{name: "just under GB", input: 1024*1024*1024 - 1, expected: "1024.0 MB"},
		{name: "exact GB boundary", input: 1024 * 1024 * 1024, expected: "1.0 GB"},
		{name: "just under TB", input: 1024*1024*1024*1024 - 1, expected: "1024.0 GB"},
		{name: "exact TB boundary", input: 1024 * 1024 * 1024 * 1024, expected: "1.0 TB"},
		{name: "large TB value", input: 5 * 1024 * 1024 * 1024 * 1024, expected: "5.0 TB"},
		{name: "A100 40GB memory", input: 42_949_672_960, expected: "40.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, formatBytes(tt.input))
		})
	}
}

func TestFormatDuration_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Duration
		expected string
	}{
		{name: "zero", input: 0, expected: "0 ms"},
		{name: "1ms", input: time.Millisecond, expected: "1 ms"},
		{name: "999ms", input: 999 * time.Millisecond, expected: "999 ms"},
		{name: "just under one second", input: time.Second - time.Millisecond, expected: "999 ms"},
		{name: "just under one minute", input: time.Minute - time.Second, expected: "59 seconds"},
		{name: "just under one hour", input: time.Hour - time.Minute, expected: "59 minutes"},
		{name: "hour with leftover minutes truncated", input: time.Hour + 45*time.Minute, expected: "1 hour"},
		{name: "24 hours", input: 24 * time.Hour, expected: "24 hours"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, formatDuration(tt.input))
		})
	}
}

func TestFormatRelativeTime_EdgeCases(t *testing.T) {
	base := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		t        time.Time
		expected string
	}{
		{name: "exact same time", t: base, expected: "0s"},
		{name: "1ms after", t: base.Add(time.Millisecond), expected: "+1ms"},
		{name: "1ms before", t: base.Add(-time.Millisecond), expected: "-1ms"},
		{name: "exactly 1 second after", t: base.Add(time.Second), expected: "+1s"},
		{name: "exactly 1 second before", t: base.Add(-time.Second), expected: "-1s"},
		{name: "exactly 1 minute after", t: base.Add(time.Minute), expected: "+1m"},
		{name: "exactly 1 hour after", t: base.Add(time.Hour), expected: "+1h"},
		{name: "large negative offset", t: base.Add(-48 * time.Hour), expected: "-48h"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, formatRelativeTime(tt.t, base))
		})
	}
}
