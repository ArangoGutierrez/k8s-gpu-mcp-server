// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ArangoGutierrez/k8s-gpu-mcp-server/pkg/blackbox"
)

// ErrAlreadyStarted is returned when Start is called on an already running watcher.
var ErrAlreadyStarted = errors.New("watcher already started")

// ErrNotStarted is returned when operations require a running watcher.
var ErrNotStarted = errors.New("watcher not started")

// XIDHandler is a callback invoked when an XID event is detected.
// Handlers are called synchronously from the watcher goroutine.
// For long-running operations, handlers should spawn their own goroutine.
type XIDHandler func(event XIDEvent)

// WatcherConfig configures the XID watcher.
type WatcherConfig struct {
	// BufferSize is the number of XID events to retain in the ring buffer.
	// Default: 100
	BufferSize int

	// KmsgPath is the path to the kernel message buffer.
	// Default: /dev/kmsg
	KmsgPath string
}

// Validate checks the configuration for errors.
func (c WatcherConfig) Validate() error {
	if c.BufferSize < 0 {
		return fmt.Errorf("buffer size must be >= 0, got %d", c.BufferSize)
	}
	return nil
}

// WithDefaults returns a copy of the config with default values applied.
func (c WatcherConfig) WithDefaults() WatcherConfig {
	if c.BufferSize == 0 {
		c.BufferSize = 100
	}
	if c.KmsgPath == "" {
		c.KmsgPath = DefaultKmsgPath
	}
	return c
}

// DefaultWatcherConfig returns sensible defaults for the XID watcher.
func DefaultWatcherConfig() WatcherConfig {
	return WatcherConfig{
		BufferSize: 100,
		KmsgPath:   DefaultKmsgPath,
	}
}

// Watcher monitors /dev/kmsg for XID errors in real-time.
// It stores events in a ring buffer and notifies registered handlers.
type Watcher struct {
	config   WatcherConfig
	parser   *Parser
	reader   *KmsgReader
	buffer   *blackbox.RingBuffer[XIDEvent]
	handlers []XIDHandler
	mu       sync.RWMutex // Protects handlers slice
	logger   *slog.Logger

	running atomic.Bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// WatcherOption configures a Watcher.
type WatcherOption func(*Watcher)

// WithWatcherLogger sets the logger for the watcher.
func WithWatcherLogger(logger *slog.Logger) WatcherOption {
	return func(w *Watcher) {
		if logger != nil {
			w.logger = logger
		}
	}
}

// NewWatcher creates a new XID watcher.
// The watcher must be started with Start() before it begins capturing events.
func NewWatcher(cfg WatcherConfig, opts ...WatcherOption) (*Watcher, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	cfg = cfg.WithDefaults()

	buffer, err := blackbox.NewRingBuffer[XIDEvent](cfg.BufferSize)
	if err != nil {
		return nil, fmt.Errorf("create buffer: %w", err)
	}

	w := &Watcher{
		config: cfg,
		parser: NewParser(),
		reader: NewKmsgReaderWithPath(cfg.KmsgPath),
		buffer: buffer,
		logger: slog.Default(),
	}

	for _, opt := range opts {
		opt(w)
	}

	return w, nil
}

// Start begins watching for XID events.
// Returns ErrAlreadyStarted if the watcher is already running.
func (w *Watcher) Start(ctx context.Context) error {
	if !w.running.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}

	// Check if kmsg is available before starting
	if !w.reader.IsAvailable() {
		w.running.Store(false)
		return fmt.Errorf("%s is not available (requires CAP_SYSLOG or root)",
			w.config.KmsgPath)
	}

	w.stopCh = make(chan struct{})

	// Start watcher goroutine
	w.wg.Add(1)
	go w.watchLoop(ctx)

	w.logger.Info("xid watcher started",
		"kmsg_path", w.config.KmsgPath,
		"buffer_size", w.config.BufferSize,
	)

	return nil
}

// Stop gracefully shuts down the watcher.
// Safe to call multiple times or if not started.
func (w *Watcher) Stop() {
	if !w.running.CompareAndSwap(true, false) {
		return
	}

	close(w.stopCh)
	w.wg.Wait()
	w.logger.Info("xid watcher stopped")
}

// IsRunning returns true if the watcher is actively watching for XID events.
func (w *Watcher) IsRunning() bool {
	return w.running.Load()
}

// RegisterHandler adds a callback for real-time XID notifications.
// Handlers are called synchronously in the watcher goroutine.
// For long-running operations, handlers should spawn their own goroutine.
func (w *Watcher) RegisterHandler(handler XIDHandler) {
	if handler == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handlers = append(w.handlers, handler)
}

// GetEvents returns XID events newer than the given timestamp.
// Events are returned in chronological order (oldest first).
func (w *Watcher) GetEvents(since time.Time) []XIDEvent {
	return w.buffer.Query(since, func(e XIDEvent) time.Time {
		return e.Timestamp
	})
}

// GetEventsByGPU returns XID events for the specified PCI bus ID.
// Events are returned in chronological order (oldest first).
func (w *Watcher) GetEventsByGPU(pciBusID string) []XIDEvent {
	normalized := normalizePCIBusID(pciBusID)
	return w.buffer.QueryFunc(func(e XIDEvent) bool {
		return e.PCIBusID == normalized
	})
}

// GetLatest returns the most recent XID event, or nil if none.
func (w *Watcher) GetLatest() *XIDEvent {
	event, ok := w.buffer.Latest()
	if !ok {
		return nil
	}
	return &event
}

// EventCount returns the current number of events in the buffer.
func (w *Watcher) EventCount() int {
	return w.buffer.Size()
}

// GetAllEvents returns all captured events.
// Events are returned in chronological order (oldest first).
func (w *Watcher) GetAllEvents() []XIDEvent {
	return w.buffer.All()
}

// watchLoop runs the main watching loop.
func (w *Watcher) watchLoop(ctx context.Context) {
	defer w.wg.Done()

	// Create a combined context that cancels on either parent context or stopCh
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Cancel watchCtx when stopCh is closed
	go func() {
		select {
		case <-w.stopCh:
			cancel()
		case <-watchCtx.Done():
		}
	}()

	// Start watching kmsg
	err := w.reader.Watch(watchCtx, w.handleMessage)
	if err != nil && watchCtx.Err() == nil {
		w.logger.Error("kmsg watch error", "error", err)
	}
}

// handleMessage processes a single kmsg line containing an XID error.
func (w *Watcher) handleMessage(message string) {
	// Parse the XID event
	event := w.parser.parseXIDLine(message)
	if event == nil {
		return
	}

	// Set timestamp to now if not parsed from message
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Add to buffer
	w.buffer.Add(*event)

	w.logger.Debug("captured xid event",
		"xid_code", event.XIDCode,
		"pci_bus_id", event.PCIBusID,
		"severity", event.Severity,
	)

	// Notify handlers
	w.notifyHandlers(*event)
}

// notifyHandlers calls all registered handlers with the event.
func (w *Watcher) notifyHandlers(event XIDEvent) {
	// Copy handlers slice to avoid holding lock during callbacks
	w.mu.RLock()
	handlers := make([]XIDHandler, len(w.handlers))
	copy(handlers, w.handlers)
	w.mu.RUnlock()

	for _, handler := range handlers {
		w.safeInvokeHandler(handler, event)
	}
}

// safeInvokeHandler calls a handler with panic recovery to prevent
// a misbehaving handler from crashing the watcher goroutine.
func (w *Watcher) safeInvokeHandler(handler XIDHandler, event XIDEvent) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("handler panic recovered",
				"panic", r,
				"xid_code", event.XIDCode,
				"pci_bus_id", event.PCIBusID,
			)
		}
	}()
	handler(event)
}
