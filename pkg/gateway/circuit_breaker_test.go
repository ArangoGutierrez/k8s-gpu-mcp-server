// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCircuitBreaker_Allow_ClosedCircuit(t *testing.T) {
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig())

	// New node should be allowed (circuit closed)
	assert.True(t, cb.Allow("node-1"))
	assert.Equal(t, CircuitClosed, cb.State("node-1"))
}

func TestCircuitBreaker_RecordFailure_OpensCircuit(t *testing.T) {
	cfg := DefaultCircuitBreakerConfig()
	cfg.Threshold = 3
	cb := NewCircuitBreaker(cfg)

	// Record failures below threshold
	cb.RecordFailure("node-1")
	cb.RecordFailure("node-1")
	assert.True(t, cb.Allow("node-1"))
	assert.Equal(t, CircuitClosed, cb.State("node-1"))

	// Record failure at threshold - circuit opens
	cb.RecordFailure("node-1")
	assert.False(t, cb.Allow("node-1"))
	assert.Equal(t, CircuitOpen, cb.State("node-1"))
}

func TestCircuitBreaker_RecordSuccess_ClosesCircuit(t *testing.T) {
	cfg := DefaultCircuitBreakerConfig()
	cfg.Threshold = 2
	cb := NewCircuitBreaker(cfg)

	// Open the circuit
	cb.RecordFailure("node-1")
	cb.RecordFailure("node-1")
	assert.False(t, cb.Allow("node-1"))

	// Success resets the circuit
	cb.RecordSuccess("node-1")
	assert.True(t, cb.Allow("node-1"))
	assert.Equal(t, CircuitClosed, cb.State("node-1"))
	assert.Equal(t, 0, cb.Failures("node-1"))
}

func TestCircuitBreaker_HalfOpen_AfterTimeout(t *testing.T) {
	cfg := CircuitBreakerConfig{
		Threshold:    2,
		ResetTimeout: 50 * time.Millisecond,
	}
	cb := NewCircuitBreaker(cfg)

	// Open the circuit
	cb.RecordFailure("node-1")
	cb.RecordFailure("node-1")
	assert.False(t, cb.Allow("node-1"))
	assert.Equal(t, CircuitOpen, cb.State("node-1"))

	// Wait for reset timeout
	time.Sleep(60 * time.Millisecond)

	// Should transition to half-open and allow request
	assert.True(t, cb.Allow("node-1"))
	assert.Equal(t, CircuitHalfOpen, cb.State("node-1"))
}

func TestCircuitBreaker_MultipleNodes(t *testing.T) {
	cfg := DefaultCircuitBreakerConfig()
	cfg.Threshold = 2
	cb := NewCircuitBreaker(cfg)

	// Open circuit for node-1
	cb.RecordFailure("node-1")
	cb.RecordFailure("node-1")
	assert.False(t, cb.Allow("node-1"))

	// node-2 should still be allowed
	assert.True(t, cb.Allow("node-2"))

	// Success on node-2
	cb.RecordSuccess("node-2")
	assert.True(t, cb.Allow("node-2"))
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cfg := DefaultCircuitBreakerConfig()
	cfg.Threshold = 2
	cb := NewCircuitBreaker(cfg)

	// Open the circuit
	cb.RecordFailure("node-1")
	cb.RecordFailure("node-1")
	assert.False(t, cb.Allow("node-1"))

	// Reset should clear state
	cb.Reset("node-1")
	assert.True(t, cb.Allow("node-1"))
	assert.Equal(t, CircuitClosed, cb.State("node-1"))
	assert.Equal(t, 0, cb.Failures("node-1"))
}

func TestCircuitState_String(t *testing.T) {
	assert.Equal(t, "closed", CircuitClosed.String())
	assert.Equal(t, "open", CircuitOpen.String())
	assert.Equal(t, "half-open", CircuitHalfOpen.String())
}

func TestDefaultCircuitBreakerConfig(t *testing.T) {
	cfg := DefaultCircuitBreakerConfig()

	assert.Equal(t, 3, cfg.Threshold)
	assert.Equal(t, 30*time.Second, cfg.ResetTimeout)
}

func TestCircuitBreaker_HalfOpen_FailureReopensCircuit(t *testing.T) {
	cfg := CircuitBreakerConfig{
		Threshold:    2,
		ResetTimeout: 50 * time.Millisecond,
	}
	cb := NewCircuitBreaker(cfg)

	// Open the circuit
	cb.RecordFailure("node-1")
	cb.RecordFailure("node-1")
	assert.Equal(t, CircuitOpen, cb.State("node-1"))

	// Wait for reset timeout to transition to half-open
	time.Sleep(60 * time.Millisecond)
	assert.True(t, cb.Allow("node-1"))
	assert.Equal(t, CircuitHalfOpen, cb.State("node-1"))

	// Another failure while half-open should re-open the circuit
	cb.RecordFailure("node-1")
	assert.Equal(t, CircuitOpen, cb.State("node-1"))
}

func TestCircuitBreaker_HalfOpen_SuccessClosesCircuit(t *testing.T) {
	cfg := CircuitBreakerConfig{
		Threshold:    2,
		ResetTimeout: 50 * time.Millisecond,
	}
	cb := NewCircuitBreaker(cfg)

	// Open the circuit
	cb.RecordFailure("node-1")
	cb.RecordFailure("node-1")
	assert.Equal(t, CircuitOpen, cb.State("node-1"))

	// Wait for reset timeout to transition to half-open
	time.Sleep(60 * time.Millisecond)
	assert.True(t, cb.Allow("node-1"))
	assert.Equal(t, CircuitHalfOpen, cb.State("node-1"))

	// Success while half-open should close the circuit
	cb.RecordSuccess("node-1")
	assert.Equal(t, CircuitClosed, cb.State("node-1"))
	assert.Equal(t, 0, cb.Failures("node-1"))
}

// --- Concurrency stress tests ---

func TestCircuitBreakerConcurrentStateTransitions(t *testing.T) {
	t.Parallel()

	cfg := CircuitBreakerConfig{
		Threshold:    5,
		ResetTimeout: 10 * time.Millisecond,
	}
	cb := NewCircuitBreaker(cfg)

	const numGoroutines = 100
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	start := make(chan struct{}) // barrier for simultaneous start

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for j := 0; j < opsPerGoroutine; j++ {
				node := "node-1"
				switch j % 4 {
				case 0:
					cb.Allow(node)
				case 1:
					cb.RecordFailure(node)
				case 2:
					cb.RecordSuccess(node)
				case 3:
					cb.State(node)
				}
			}
		}(i)
	}

	close(start) // release all goroutines simultaneously
	wg.Wait()

	// Verify state is valid (one of the three valid states)
	state := cb.State("node-1")
	assert.True(t, state == CircuitClosed || state == CircuitOpen || state == CircuitHalfOpen,
		"state should be valid, got %v", state)

	// Failures count must be non-negative
	assert.True(t, cb.Failures("node-1") >= 0, "failures must be non-negative")
}

func TestCircuitBreakerHalfOpenTransition(t *testing.T) {
	t.Parallel()

	cfg := CircuitBreakerConfig{
		Threshold:    2,
		ResetTimeout: 1 * time.Millisecond, // very short so Allow() triggers half-open
	}
	cb := NewCircuitBreaker(cfg)

	// Open the circuit
	cb.RecordFailure("node-1")
	cb.RecordFailure("node-1")
	assert.Equal(t, CircuitOpen, cb.State("node-1"))

	// Wait for reset timeout so next Allow() transitions to half-open
	time.Sleep(5 * time.Millisecond)

	const numGoroutines = 50
	var wg sync.WaitGroup
	start := make(chan struct{})

	// Track results
	successes := make([]bool, numGoroutines)
	failures := make([]bool, numGoroutines)

	// Half the goroutines try Allow + RecordSuccess, half try Allow + RecordFailure
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			allowed := cb.Allow("node-1")
			if id%2 == 0 {
				cb.RecordSuccess("node-1")
				successes[id] = allowed
			} else {
				cb.RecordFailure("node-1")
				failures[id] = allowed
			}
		}(i)
	}

	close(start)
	wg.Wait()

	// Final state must be valid
	state := cb.State("node-1")
	assert.True(t, state == CircuitClosed || state == CircuitOpen || state == CircuitHalfOpen,
		"state should be valid after concurrent half-open transitions, got %v", state)
}

func TestCircuitBreakerConcurrentNodeAccess(t *testing.T) {
	t.Parallel()

	cfg := CircuitBreakerConfig{
		Threshold:    3,
		ResetTimeout: 50 * time.Millisecond,
	}

	var stateChanges sync.Map // track callback invocations safely

	cfg.OnStateChange = func(node string, state int, healthy bool) {
		stateChanges.Store(node+"-latest", state)
	}

	cb := NewCircuitBreaker(cfg)

	const numNodes = 20
	const numGoroutines = 50

	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			node := fmt.Sprintf("node-%d", id%numNodes)
			for j := 0; j < 50; j++ {
				cb.Allow(node)
				if j%3 == 0 {
					cb.RecordFailure(node)
				} else {
					cb.RecordSuccess(node)
				}
				cb.Failures(node)
				cb.State(node)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	// Verify all nodes are in valid states
	for i := 0; i < numNodes; i++ {
		node := fmt.Sprintf("node-%d", i)
		state := cb.State(node)
		assert.True(t, state == CircuitClosed || state == CircuitOpen || state == CircuitHalfOpen,
			"node %s has invalid state %v", node, state)
		assert.True(t, cb.Failures(node) >= 0,
			"node %s has negative failures", node)
	}
}
