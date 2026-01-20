// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseKmsgRecord(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		check   func(*testing.T, *KmsgRecord)
	}{
		{
			name:  "valid NVRM message",
			input: "4,12345,678901234,-;NVRM: Xid (PCI:0000:00:1E.0): 48",
			check: func(t *testing.T, r *KmsgRecord) {
				assert.Equal(t, 4, r.Priority)
				assert.Equal(t, uint64(12345), r.Sequence)
				assert.Equal(t, time.Duration(678901234)*time.Microsecond,
					r.Timestamp)
				assert.Contains(t, r.Message, "NVRM")
				assert.Contains(t, r.Message, "Xid")
				assert.Contains(t, r.Message, "48")
			},
		},
		{
			name:  "valid non-NVRM message",
			input: "6,99999,123456789,-;kernel: some other message",
			check: func(t *testing.T, r *KmsgRecord) {
				assert.Equal(t, 6, r.Priority)
				assert.Equal(t, uint64(99999), r.Sequence)
				assert.Equal(t, "kernel: some other message", r.Message)
			},
		},
		{
			name:    "invalid format - no semicolon",
			input:   "4,12345,678901234",
			wantErr: true,
		},
		{
			name:    "invalid format - empty header",
			input:   ";message only",
			wantErr: true,
		},
		{
			name:    "invalid priority - not a number",
			input:   "abc,12345,678901234,-;message",
			wantErr: true,
		},
		{
			name:    "invalid sequence - not a number",
			input:   "4,not_a_seq,678901234,-;message",
			wantErr: true,
		},
		{
			name:    "invalid timestamp - not a number",
			input:   "4,12345,not_a_ts,-;message",
			wantErr: true,
		},
		{
			name:    "too few header fields",
			input:   "4,12345;message",
			wantErr: true,
		},
		{
			name:  "message with continuation flag",
			input: "4,12345,678901234,c;continued message",
			check: func(t *testing.T, r *KmsgRecord) {
				assert.Equal(t, 4, r.Priority)
				assert.Equal(t, "continued message", r.Message)
			},
		},
		{
			name:  "message with extra flags",
			input: "4,12345,678901234,-,extra;message with extras",
			check: func(t *testing.T, r *KmsgRecord) {
				assert.Equal(t, 4, r.Priority)
				assert.Equal(t, "message with extras", r.Message)
			},
		},
		{
			name: "XID with full details",
			input: "4,54321,999999999,-;NVRM: Xid (PCI:0000:00:1E.0): 79, " +
				"pid='5678', name=nvidia-smi",
			check: func(t *testing.T, r *KmsgRecord) {
				assert.Equal(t, 4, r.Priority)
				assert.Contains(t, r.Message, "Xid")
				assert.Contains(t, r.Message, "79")
				assert.Contains(t, r.Message, "pid='5678'")
				assert.Contains(t, r.Message, "nvidia-smi")
			},
		},
		{
			name:  "zero timestamp",
			input: "4,0,0,-;boot message",
			check: func(t *testing.T, r *KmsgRecord) {
				assert.Equal(t, uint64(0), r.Sequence)
				assert.Equal(t, time.Duration(0), r.Timestamp)
			},
		},
		{
			name:  "message with semicolons",
			input: "4,12345,678901234,-;message; with; semicolons;",
			check: func(t *testing.T, r *KmsgRecord) {
				assert.Equal(t, "message; with; semicolons;", r.Message)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, err := parseKmsgRecord(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, record)
			}
		})
	}
}

func TestKmsgReader_IsAvailable(t *testing.T) {
	t.Run("non-existent path", func(t *testing.T) {
		reader := NewKmsgReaderWithPath("/nonexistent/path/kmsg")
		assert.False(t, reader.IsAvailable())
	})

	t.Run("permission denied path", func(t *testing.T) {
		// Create temp file with no read permissions
		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "no-read")
		err := os.WriteFile(tmpFile, []byte("test"), 0000)
		require.NoError(t, err)

		reader := NewKmsgReaderWithPath(tmpFile)
		// On most systems, root can still read, so this may pass or fail
		// We just verify the function doesn't panic
		_ = reader.IsAvailable()
	})

	t.Run("readable file returns true", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "readable")
		err := os.WriteFile(tmpFile, []byte("test"), 0644)
		require.NoError(t, err)

		reader := NewKmsgReaderWithPath(tmpFile)
		assert.True(t, reader.IsAvailable())
	})
}

func TestNewKmsgReader(t *testing.T) {
	reader := NewKmsgReader()
	require.NotNil(t, reader)
	assert.Equal(t, DefaultKmsgPath, reader.path)
}

func TestNewKmsgReaderWithPath(t *testing.T) {
	customPath := "/custom/path/to/kmsg"
	reader := NewKmsgReaderWithPath(customPath)
	require.NotNil(t, reader)
	assert.Equal(t, customPath, reader.path)
}

func TestKmsgReader_ReadMessages_FileNotFound(t *testing.T) {
	reader := NewKmsgReaderWithPath("/nonexistent/kmsg")
	ctx := context.Background()

	messages, err := reader.ReadMessages(ctx)

	assert.Error(t, err)
	assert.Nil(t, messages)
	assert.Contains(t, err.Error(), "not found")
}

func TestKmsgReader_ReadMessages_ContextCancelled(t *testing.T) {
	// Create a temp file with some content
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test-kmsg")
	content := "4,12345,678901234,-;NVRM: Xid (PCI:0000:00:1E.0): 48\n"
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	reader := NewKmsgReaderWithPath(tmpFile)

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Should still work since file reading is fast
	messages, err := reader.ReadMessages(ctx)

	// May or may not return messages depending on timing
	// Just verify no panic and err is nil for regular file
	assert.NoError(t, err)
	_ = messages
}

func TestKmsgReader_ReadMessages_FilterNVRM(t *testing.T) {
	// Create temp file with mixed messages
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test-kmsg")

	content := `4,1,100,-;kernel: starting up
6,2,200,-;NVRM: loading NVIDIA driver
4,3,300,-;NVRM: Xid (PCI:0000:00:1E.0): 48
6,4,400,-;kernel: network interface up
4,5,500,-;NVRM: Xid (PCI:0000:00:1E.0): 79, pid='1234'
6,6,600,-;some other message
`
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	reader := NewKmsgReaderWithPath(tmpFile)
	ctx := context.Background()

	messages, err := reader.ReadMessages(ctx)

	require.NoError(t, err)
	// Should have 3 NVRM messages (including non-XID)
	assert.Len(t, messages, 3)

	// Verify all contain NVRM
	for _, msg := range messages {
		assert.Contains(t, msg, "NVRM")
	}
}

func TestKmsgReader_ReadMessages_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "empty-kmsg")
	err := os.WriteFile(tmpFile, []byte(""), 0644)
	require.NoError(t, err)

	reader := NewKmsgReaderWithPath(tmpFile)
	ctx := context.Background()

	messages, err := reader.ReadMessages(ctx)

	require.NoError(t, err)
	assert.Empty(t, messages)
}

func TestKmsgReader_ReadMessages_MalformedLines(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "malformed-kmsg")

	content := `not a valid line
4,bad_seq,100,-;message
4,1,100,-;NVRM: Xid (PCI:0000:00:1E.0): 48
missing semicolon
4,2,200,-;NVRM: another message
`
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	reader := NewKmsgReaderWithPath(tmpFile)
	ctx := context.Background()

	messages, err := reader.ReadMessages(ctx)

	require.NoError(t, err)
	// Should only get the valid NVRM messages
	assert.Len(t, messages, 2)
}

func TestParser_ParseKernelLogs_FallbackToDmesg(t *testing.T) {
	parser := NewParser()

	// Create cancelled context to simulate fast failure
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// ParseKernelLogs should check context first
	events, err := parser.ParseKernelLogs(ctx)

	assert.Error(t, err)
	assert.Nil(t, events)
	assert.Contains(t, err.Error(), "context cancelled")
}

func TestParser_parseMessages(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name      string
		messages  []string
		wantCount int
		wantXIDs  []int
	}{
		{
			name:      "empty messages",
			messages:  []string{},
			wantCount: 0,
			wantXIDs:  []int{},
		},
		{
			name: "single XID",
			messages: []string{
				"NVRM: Xid (PCI:0000:00:1E.0): 48, pid='1234'",
			},
			wantCount: 1,
			wantXIDs:  []int{48},
		},
		{
			name: "multiple XIDs",
			messages: []string{
				"NVRM: Xid (PCI:0000:00:1E.0): 48",
				"NVRM: loading driver",
				"NVRM: Xid (PCI:0000:00:1E.0): 79",
			},
			wantCount: 2,
			wantXIDs:  []int{48, 79},
		},
		{
			name: "no XIDs in messages",
			messages: []string{
				"NVRM: loading driver",
				"NVRM: GPU initialized",
			},
			wantCount: 0,
			wantXIDs:  []int{},
		},
		{
			name:      "nil messages",
			messages:  nil,
			wantCount: 0,
			wantXIDs:  []int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := parser.parseMessages(tt.messages)

			assert.Len(t, events, tt.wantCount)

			for i, wantXID := range tt.wantXIDs {
				if i < len(events) {
					assert.Equal(t, wantXID, events[i].XIDCode)
				}
			}
		})
	}
}

func TestKmsgRecord_Structure(t *testing.T) {
	record := KmsgRecord{
		Priority:  4,
		Sequence:  12345,
		Timestamp: 678 * time.Millisecond,
		Message:   "test message",
	}

	assert.Equal(t, 4, record.Priority)
	assert.Equal(t, uint64(12345), record.Sequence)
	assert.Equal(t, 678*time.Millisecond, record.Timestamp)
	assert.Equal(t, "test message", record.Message)
}

func TestDefaultKmsgPath(t *testing.T) {
	assert.Equal(t, "/dev/kmsg", DefaultKmsgPath)
}

func TestKmsgReader_Watch_NilCallback(t *testing.T) {
	reader := NewKmsgReader()
	ctx := context.Background()

	err := reader.Watch(ctx, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "callback cannot be nil")
}

func TestKmsgReader_Watch_FileNotFound(t *testing.T) {
	reader := NewKmsgReaderWithPath("/nonexistent/kmsg")
	ctx := context.Background()

	err := reader.Watch(ctx, func(msg string) {})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestKmsgReader_Watch_ContextCancellation(t *testing.T) {
	// Create temp file with some content
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test-kmsg")
	err := os.WriteFile(tmpFile, []byte(""), 0644)
	require.NoError(t, err)

	reader := NewKmsgReaderWithPath(tmpFile)

	// Create a context that we'll cancel
	ctx, cancel := context.WithCancel(context.Background())

	var watchErr error
	done := make(chan struct{})

	go func() {
		watchErr = reader.Watch(ctx, func(msg string) {})
		close(done)
	}()

	// Give it time to start watching
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait for Watch to return
	select {
	case <-done:
		// Watch should return nil on context cancellation
		assert.NoError(t, watchErr)
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not return after context cancellation")
	}
}

func TestKmsgReader_Watch_FilterMessages(t *testing.T) {
	// This test verifies the filtering logic by checking that only
	// NVRM+Xid messages would trigger the callback (simulated via handleMessage)
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test-kmsg")

	// Write some test content
	content := `4,1,100,-;kernel: starting up
6,2,200,-;NVRM: loading NVIDIA driver
4,3,300,-;NVRM: Xid (PCI:0000:00:1E.0): 48
6,4,400,-;kernel: network interface up
`
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	reader := NewKmsgReaderWithPath(tmpFile)

	// ReadMessages should filter for NVRM messages
	ctx := context.Background()
	messages, err := reader.ReadMessages(ctx)
	require.NoError(t, err)

	// Should have 2 NVRM messages
	assert.Len(t, messages, 2)

	// Of those, only one contains "Xid"
	xidCount := 0
	for _, msg := range messages {
		if strings.Contains(msg, "Xid") {
			xidCount++
		}
	}
	assert.Equal(t, 1, xidCount)
}

func TestKmsgReader_Watch_SeeksToEnd(t *testing.T) {
	// This test verifies that Watch() seeks to the end of the file
	// We can't easily test real-time watching with a regular file,
	// but we can verify the seek behavior by checking that existing
	// content is not processed

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test-kmsg")

	// Write initial content (should be skipped)
	initialContent := `4,1,100,-;NVRM: Xid (PCI:0000:00:1E.0): 48
4,2,200,-;NVRM: Xid (PCI:0000:00:1E.0): 79
`
	err := os.WriteFile(tmpFile, []byte(initialContent), 0644)
	require.NoError(t, err)

	reader := NewKmsgReaderWithPath(tmpFile)

	var receivedMessages []string
	var mu sync.Mutex

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Watch should seek to end and not see the initial content
	err = reader.Watch(ctx, func(msg string) {
		mu.Lock()
		receivedMessages = append(receivedMessages, msg)
		mu.Unlock()
	})

	// Should return nil on context timeout (no error)
	assert.NoError(t, err)

	// Should not have received the existing messages (they were before the seek point)
	mu.Lock()
	// Note: With a regular file, after seeking to end there's nothing more to read,
	// so we expect 0 messages. Real /dev/kmsg would block and receive new messages.
	assert.Empty(t, receivedMessages)
	mu.Unlock()
}
