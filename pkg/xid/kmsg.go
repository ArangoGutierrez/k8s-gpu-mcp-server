// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultKmsgPath is the default path to the kernel message buffer.
	DefaultKmsgPath = "/dev/kmsg"

	// kmsgReadTimeout is the maximum time to spend reading from /dev/kmsg.
	kmsgReadTimeout = 5 * time.Second
)

// KmsgReader reads kernel messages from /dev/kmsg.
type KmsgReader struct {
	path string
}

// NewKmsgReader creates a new reader for the kernel message buffer.
func NewKmsgReader() *KmsgReader {
	return &KmsgReader{path: DefaultKmsgPath}
}

// NewKmsgReaderWithPath creates a reader with a custom path (for testing).
func NewKmsgReaderWithPath(path string) *KmsgReader {
	return &KmsgReader{path: path}
}

// KmsgRecord represents a parsed /dev/kmsg record.
type KmsgRecord struct {
	Priority  int
	Sequence  uint64
	Timestamp time.Duration // Microseconds since boot
	Message   string
}

// ReadMessages reads all available messages from /dev/kmsg.
// Returns messages filtered to only NVRM (NVIDIA driver) entries.
func (r *KmsgReader) ReadMessages(ctx context.Context) ([]string, error) {
	// Check if /dev/kmsg exists and is readable
	if _, err := os.Stat(r.path); os.IsNotExist(err) {
		return nil, fmt.Errorf("%s not found: %w", r.path, err)
	}

	// Open /dev/kmsg for reading
	// Use O_RDONLY | O_NONBLOCK to avoid blocking on empty buffer
	file, err := os.OpenFile(r.path, os.O_RDONLY, 0)
	if err != nil {
		if os.IsPermission(err) {
			return nil, fmt.Errorf("permission denied reading %s "+
				"(requires CAP_SYSLOG or root): %w", r.path, err)
		}
		return nil, fmt.Errorf("failed to open %s: %w", r.path, err)
	}
	// Note: file is closed explicitly in the timeout case; defer handles normal path
	defer func() {
		// Close is idempotent, safe to call even if already closed
		_ = file.Close()
	}()

	// Seek to start to read all available messages
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek in %s: %w", r.path, err)
	}

	// Set up timeout context
	readCtx, cancel := context.WithTimeout(ctx, kmsgReadTimeout)
	defer cancel()

	// Channel for results from scanner goroutine
	type scanResult struct {
		messages []string
		err      error
	}
	resultCh := make(chan scanResult, 1)

	// Read messages in a goroutine so we can cancel via context
	go func() {
		var messages []string
		scanner := bufio.NewScanner(file)

		for scanner.Scan() {
			// Check context before processing
			select {
			case <-readCtx.Done():
				resultCh <- scanResult{messages: messages}
				return
			default:
			}

			line := scanner.Text()
			record, err := parseKmsgRecord(line)
			if err != nil {
				continue // Skip malformed records
			}

			// Filter for NVIDIA driver messages only
			if strings.Contains(record.Message, "NVRM") {
				messages = append(messages, record.Message)
			}
		}

		var scanErr error
		if err := scanner.Err(); err != nil {
			// EAGAIN is expected for non-blocking read; ignore it
			if !strings.Contains(err.Error(), "resource temporarily unavailable") {
				scanErr = fmt.Errorf("error reading %s: %w", r.path, err)
			}
		}
		resultCh <- scanResult{messages: messages, err: scanErr}
	}()

	// Wait for completion or context cancellation
	select {
	case result := <-resultCh:
		// Goroutine completed normally
		return result.messages, result.err

	case <-readCtx.Done():
		// Timeout or context cancelled
		// Close the file to interrupt blocking read in goroutine
		// This ensures the goroutine doesn't leak
		if err := file.Close(); err != nil {
			// Log but don't return error - we already have our messages
			_ = err // Intentionally ignored as we're shutting down
		}

		// Drain the result channel to avoid goroutine leak
		result := <-resultCh
		return result.messages, nil
	}
}

// parseKmsgRecord parses a single /dev/kmsg record.
// Format: priority,sequence,timestamp,flags;message
func parseKmsgRecord(line string) (*KmsgRecord, error) {
	// Split on semicolon to separate header from message
	parts := strings.SplitN(line, ";", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid kmsg format: missing semicolon")
	}

	header := parts[0]
	message := parts[1]

	// Parse header fields: priority,sequence,timestamp,flags
	fields := strings.Split(header, ",")
	if len(fields) < 3 {
		return nil, fmt.Errorf("invalid kmsg header: expected 3+ fields")
	}

	priority, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil, fmt.Errorf("invalid priority: %w", err)
	}

	seq, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid sequence: %w", err)
	}

	tsUsec, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp: %w", err)
	}

	return &KmsgRecord{
		Priority:  priority,
		Sequence:  seq,
		Timestamp: time.Duration(tsUsec) * time.Microsecond,
		Message:   message,
	}, nil
}

// IsAvailable checks if /dev/kmsg is readable.
func (r *KmsgReader) IsAvailable() bool {
	file, err := os.OpenFile(r.path, os.O_RDONLY, 0)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

// Watch monitors /dev/kmsg for new messages in real-time.
// It seeks to the end of the file and continuously reads new messages,
// calling the callback for each NVRM message containing "Xid".
//
// Watch blocks until the context is cancelled or an error occurs.
// Returns nil on context cancellation, error otherwise.
//
// Note: /dev/kmsg requires CAP_SYSLOG or root privileges.
func (r *KmsgReader) Watch(ctx context.Context, callback func(string)) error {
	if callback == nil {
		return fmt.Errorf("callback cannot be nil")
	}

	// Check if /dev/kmsg exists and is readable
	if _, err := os.Stat(r.path); os.IsNotExist(err) {
		return fmt.Errorf("%s not found: %w", r.path, err)
	}

	// Open /dev/kmsg for reading
	file, err := os.OpenFile(r.path, os.O_RDONLY, 0)
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied reading %s "+
				"(requires CAP_SYSLOG or root): %w", r.path, err)
		}
		return fmt.Errorf("failed to open %s: %w", r.path, err)
	}
	defer func() {
		_ = file.Close() // Intentionally ignore close error on read-only file
	}()

	// Seek to end to only read new messages
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("failed to seek to end of %s: %w", r.path, err)
	}

	// Create scanner for line-by-line reading
	scanner := bufio.NewScanner(file)

	// Polling interval for non-blocking behavior
	const pollInterval = 100 * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// Try to read next line
		if scanner.Scan() {
			line := scanner.Text()
			record, err := parseKmsgRecord(line)
			if err != nil {
				continue // Skip malformed records
			}

			// Filter for NVIDIA XID messages only
			if strings.Contains(record.Message, "NVRM") &&
				strings.Contains(record.Message, "Xid") {
				callback(record.Message)
			}
		} else {
			// No data available or error
			if err := scanner.Err(); err != nil {
				// EAGAIN is expected for non-blocking read
				if strings.Contains(err.Error(), "resource temporarily unavailable") {
					// Reset scanner after EAGAIN
					scanner = bufio.NewScanner(file)
					time.Sleep(pollInterval)
					continue
				}
				// Real error
				return fmt.Errorf("error reading %s: %w", r.path, err)
			}
			// No more data, wait before retrying
			time.Sleep(pollInterval)
			// Reset scanner to pick up new data
			scanner = bufio.NewScanner(file)
		}
	}
}
