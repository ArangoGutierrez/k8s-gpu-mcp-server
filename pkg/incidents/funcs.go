// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package incidents

import (
	"fmt"
	"text/template"
	"time"
)

// templateFuncs provides helper functions for templates.
var templateFuncs = template.FuncMap{
	"bytes":        formatBytes,
	"duration":     formatDuration,
	"relativeTime": formatRelativeTime,
}

// formatBytes converts bytes to human-readable format (e.g., "15.2 GB").
func formatBytes(b uint64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)

	switch {
	case b >= TB:
		return fmt.Sprintf("%.1f TB", float64(b)/float64(TB))
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d bytes", b)
	}
}

// formatDuration converts duration to human-readable format (e.g., "3 minutes").
func formatDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	case d >= time.Minute:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", mins)
	case d >= time.Second:
		secs := int(d.Seconds())
		if secs == 1 {
			return "1 second"
		}
		return fmt.Sprintf("%d seconds", secs)
	default:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}
}

// formatRelativeTime formats a time relative to a base (e.g., "-5m", "+30s").
func formatRelativeTime(t, base time.Time) string {
	d := t.Sub(base)

	if d == 0 {
		return "0s"
	}

	sign := "+"
	if d < 0 {
		sign = "-"
		d = -d
	}

	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%s%dh", sign, int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%s%dm", sign, int(d.Minutes()))
	case d >= time.Second:
		return fmt.Sprintf("%s%ds", sign, int(d.Seconds()))
	default:
		return fmt.Sprintf("%s%dms", sign, d.Milliseconds())
	}
}
