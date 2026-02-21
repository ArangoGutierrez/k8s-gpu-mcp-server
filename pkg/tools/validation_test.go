// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsValidNodeName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "valid simple name",
			input: "node1",
			want:  true,
		},
		{
			name:  "valid DNS subdomain with dots",
			input: "node1.example.com",
			want:  true,
		},
		{
			name:  "valid name with dashes",
			input: "gpu-node-01",
			want:  true,
		},
		{
			name:  "valid GKE style name",
			input: "gke-cluster-gpu-pool-abc12345-xyz",
			want:  true,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
		{
			name:  "starts with dash",
			input: "-invalid",
			want:  false,
		},
		{
			name:  "ends with dash",
			input: "invalid-",
			want:  false,
		},
		{
			name:  "contains uppercase",
			input: "Node1",
			want:  false,
		},
		{
			name:  "contains underscore",
			input: "node_1",
			want:  false,
		},
		{
			name:  "contains space",
			input: "node 1",
			want:  false,
		},
		{
			name:  "contains special char",
			input: "node!1",
			want:  false,
		},
		{
			name:  "too long",
			input: strings.Repeat("a", 254),
			want:  false,
		},
		{
			name:  "max length valid",
			input: strings.Repeat("a", 253),
			want:  true,
		},
		{
			name:  "path injection attempt",
			input: "../etc/passwd",
			want:  false,
		},
		{
			name:  "dot segment",
			input: "node..name",
			want:  false,
		},
		// FieldSelector injection payloads
		{
			name:  "field selector injection with comma",
			input: "node-1,spec.status.phase=Failed",
			want:  false,
		},
		{
			name:  "SQL injection attempt with semicolon",
			input: "node-1;drop table",
			want:  false,
		},
		{
			name:  "field selector injection with equals",
			input: "node-1=value",
			want:  false,
		},
		{
			name:  "newline injection",
			input: "node-1\nmalicious",
			want:  false,
		},
		{
			name:  "null byte injection",
			input: "node-1\x00malicious",
			want:  false,
		},
		{
			name:  "backtick injection",
			input: "node-1`whoami`",
			want:  false,
		},
		{
			name:  "dollar sign injection",
			input: "node-1$(id)",
			want:  false,
		},
		{
			name:  "pipe injection",
			input: "node-1|cat /etc/passwd",
			want:  false,
		},
		{
			name:  "ampersand injection",
			input: "node-1&& rm -rf /",
			want:  false,
		},
		{
			name:  "curly brace injection",
			input: "node-1{bad}",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidNodeName(tt.input)
			assert.Equal(t, tt.want, got, "isValidNodeName(%q)", tt.input)
		})
	}
}
