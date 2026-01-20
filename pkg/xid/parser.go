// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"k8s.io/klog/v2"
)

// XIDEvent represents a parsed XID error event from kernel logs.
type XIDEvent struct {
	Timestamp   time.Time `json:"timestamp"`
	XIDCode     int       `json:"xid_code"`
	PCIBusID    string    `json:"pci_bus_id"`
	GPUIndex    int       `json:"gpu_index"`
	GPUUUID     string    `json:"gpu_uuid,omitempty"` // Resolved GPU UUID (set by consumer)
	PID         int       `json:"pid,omitempty"`
	ProcessName string    `json:"process_name,omitempty"`
	PodName     string    `json:"pod_name,omitempty"`  // Correlated K8s pod name
	Namespace   string    `json:"namespace,omitempty"` // K8s namespace
	RawMessage  string    `json:"raw_message"`
	Severity    string    `json:"severity,omitempty"`    // From XID code lookup (info/warning/critical/fatal)
	Description string    `json:"description,omitempty"` // Human-readable description from codes.go
}

// GetTimestamp implements the blackbox.Timestamped interface.
func (e XIDEvent) GetTimestamp() time.Time {
	return e.Timestamp
}

// EnrichEvent populates Severity and Description from the XID code lookup table.
// This is called automatically by the Watcher, but can be called manually
// for events parsed via ParseKernelLogs().
func EnrichEvent(event *XIDEvent) {
	if event == nil {
		return
	}
	info := LookupOrUnknown(event.XIDCode)
	event.Severity = info.Severity
	event.Description = info.Description
}

// Parser extracts XID error events from kernel dmesg output.
type Parser struct {
	// xidRegex matches lines like:
	// NVRM: Xid (PCI:0000:00:1E.0): 48, pid='1234', name=python3
	xidRegex *regexp.Regexp

	// pidRegex extracts PID from XID line
	pidRegex *regexp.Regexp

	// processNameRegex extracts process name from XID line
	processNameRegex *regexp.Regexp

	// timestampRegex extracts kernel timestamp [seconds.microseconds]
	timestampRegex *regexp.Regexp
}

// NewParser creates a new XID parser with compiled regex patterns.
func NewParser() *Parser {
	return &Parser{
		xidRegex:         regexp.MustCompile(`Xid \(PCI:([0-9a-fA-F:\.]+)\):\s*(\d+)`),
		pidRegex:         regexp.MustCompile(`pid[=']+(\d+)`),
		processNameRegex: regexp.MustCompile(`name[=']+([^',\s]+)`),
		timestampRegex:   regexp.MustCompile(`^\[\s*(\d+\.\d+)\]`),
	}
}

// ParseKernelLogs reads XID events from kernel logs.
// Prefers /dev/kmsg when available, falls back to dmesg command.
func (p *Parser) ParseKernelLogs(ctx context.Context) ([]XIDEvent, error) {
	// Check context before expensive operation
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled: %w", err)
	}

	// Try /dev/kmsg first (works in distroless containers)
	kmsgReader := NewKmsgReader()
	kmsgAvailable := kmsgReader.IsAvailable()

	if kmsgAvailable {
		klog.V(4).InfoS("reading kernel logs from /dev/kmsg")
		messages, err := kmsgReader.ReadMessages(ctx)
		if err == nil {
			klog.V(4).InfoS("read kernel messages",
				"count", len(messages), "source", "/dev/kmsg")
			return p.parseMessages(messages), nil
		}
		// Log warning and fall back to dmesg
		klog.V(2).InfoS("failed to read /dev/kmsg, falling back to dmesg",
			"error", err)
	} else {
		klog.V(4).InfoS("/dev/kmsg not available, using dmesg")
	}

	// Fall back to dmesg command
	events, err := p.ParseDmesg(ctx)
	if err != nil {
		// If both methods failed, provide a helpful error message
		if !kmsgAvailable {
			return nil, fmt.Errorf("%w. "+
				"Note: /dev/kmsg was not accessible. In Kubernetes, this requires "+
				"securityContext.privileged=true due to cgroup v2 device restrictions. "+
				"Ensure the Helm chart has xidAnalysis.enabled=true and "+
				"securityContext.privileged=true", err)
		}
		return nil, err
	}
	return events, nil
}

// parseMessages extracts XID events from a slice of kernel log messages.
func (p *Parser) parseMessages(messages []string) []XIDEvent {
	var events []XIDEvent
	for _, msg := range messages {
		if !strings.Contains(msg, "Xid") {
			continue
		}
		if event := p.parseXIDLine(msg); event != nil {
			events = append(events, *event)
		}
	}
	return events
}

// ParseDmesg executes dmesg and parses XID error events.
// Returns empty slice if no XIDs found or if dmesg is not accessible.
// Deprecated: Use ParseKernelLogs instead, which prefers /dev/kmsg.
func (p *Parser) ParseDmesg(ctx context.Context) ([]XIDEvent, error) {
	// Check context before expensive operation
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled: %w", err)
	}

	// Execute dmesg with context cancellation support
	// --raw: output raw log buffer (no formatting)
	// --level=err,warn: only show error and warning messages
	cmd := exec.CommandContext(ctx, "dmesg", "--raw", "--level=err,warn")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if this is a permission error
		if strings.Contains(string(output), "Permission denied") ||
			strings.Contains(err.Error(), "permission denied") {
			return nil, fmt.Errorf("failed to read dmesg: permission denied "+
				"(try running with sudo or as root): %w", err)
		}

		// Check if dmesg command not found
		if strings.Contains(err.Error(), "not found") ||
			strings.Contains(err.Error(), "executable file not found") {
			return nil, fmt.Errorf("dmesg command not found: %w", err)
		}

		// Generic error
		return nil, fmt.Errorf("failed to execute dmesg: %w", err)
	}

	// Parse the output
	return p.parseDmesgOutput(string(output)), nil
}

// parseDmesgOutput extracts XID events from dmesg text output.
func (p *Parser) parseDmesgOutput(output string) []XIDEvent {
	var events []XIDEvent

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		// Skip lines that don't contain NVIDIA driver messages
		if !strings.Contains(line, "NVRM") {
			continue
		}

		// Skip lines that don't contain XID errors
		if !strings.Contains(line, "Xid") {
			continue
		}

		// Try to parse this line as an XID event
		if event := p.parseXIDLine(line); event != nil {
			events = append(events, *event)
		}
	}

	return events
}

// parseXIDLine extracts XID event details from a single dmesg line.
// Returns nil if the line cannot be parsed.
func (p *Parser) parseXIDLine(line string) *XIDEvent {
	// Extract XID code and PCI bus ID
	matches := p.xidRegex.FindStringSubmatch(line)
	if len(matches) < 3 {
		return nil
	}

	pciBusID := matches[1]
	xidCode, err := strconv.Atoi(matches[2])
	if err != nil {
		return nil
	}

	event := &XIDEvent{
		XIDCode:    xidCode,
		PCIBusID:   normalizePCIBusID(pciBusID),
		RawMessage: line,
		GPUIndex:   -1, // Will be filled by tool handler
	}

	// Extract timestamp if present
	// Format: [seconds.microseconds] from boot
	if tsMatches := p.timestampRegex.FindStringSubmatch(line); len(tsMatches) >= 2 {
		if seconds, err := strconv.ParseFloat(tsMatches[1], 64); err == nil {
			// Convert seconds since boot to approximate timestamp
			// Note: This is relative to system boot time, not absolute time
			// For production use, would need to get boot time from /proc/uptime
			event.Timestamp = time.Unix(int64(seconds), 0)
		}
	}

	// Extract PID if present
	if pidMatches := p.pidRegex.FindStringSubmatch(line); len(pidMatches) >= 2 {
		event.PID = parseInt(pidMatches[1])
	}

	// Extract process name if present
	if nameMatches := p.processNameRegex.FindStringSubmatch(line); len(nameMatches) >= 2 {
		event.ProcessName = nameMatches[1]
	}

	// Enrich with severity and description from codes.go
	EnrichEvent(event)

	return event
}

// parseInt safely parses an integer string, returning 0 on error.
// This is suitable for optional fields like PID where 0 is an acceptable
// default (PIDs start at 1, so 0 is never a valid PID).
func parseInt(s string) int {
	val, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return val
}

// normalizePCIBusID normalizes PCI bus ID to canonical format.
// Examples:
//   - "0000:00:1E.0" -> "0000:00:1E.0" (already canonical)
//   - "00:1E.0" -> "0000:00:1E.0" (add domain prefix)
//   - "0:1e.0" -> "0000:00:1E.0" (normalize hex case and padding)
func normalizePCIBusID(busID string) string {
	// Convert to uppercase for hex consistency
	busID = strings.ToUpper(busID)

	// If already has domain prefix (0000:), return as-is
	if strings.Count(busID, ":") == 2 {
		return busID
	}

	// Add domain prefix if missing
	if strings.Count(busID, ":") == 1 {
		return "0000:" + busID
	}

	// Return as-is if format is unexpected
	return busID
}
