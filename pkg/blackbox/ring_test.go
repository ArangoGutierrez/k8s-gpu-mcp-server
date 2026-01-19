// Copyright 2026 k8s-gpu-mcp-server contributors
// SPDX-License-Identifier: Apache-2.0

package blackbox

import (
	"sync"
	"testing"
	"time"
)

// timestampedItem is a test type that implements Timestamped.
type timestampedItem struct {
	ts    time.Time
	value int
}

func (t timestampedItem) GetTimestamp() time.Time {
	return t.ts
}

// mustNewRingBuffer creates a ring buffer or panics. For tests only.
func mustNewRingBuffer[T any](capacity int) *RingBuffer[T] {
	buf, err := NewRingBuffer[T](capacity)
	if err != nil {
		panic(err)
	}
	return buf
}

func TestNewRingBuffer(t *testing.T) {
	t.Parallel()

	t.Run("valid capacity", func(t *testing.T) {
		t.Parallel()
		buf, err := NewRingBuffer[int](10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if buf.Capacity() != 10 {
			t.Errorf("got capacity %d, want 10", buf.Capacity())
		}
		if buf.Size() != 0 {
			t.Errorf("got size %d, want 0", buf.Size())
		}
	})

	t.Run("error on zero capacity", func(t *testing.T) {
		t.Parallel()
		_, err := NewRingBuffer[int](0)
		if err != ErrInvalidCapacity {
			t.Errorf("got error %v, want ErrInvalidCapacity", err)
		}
	})

	t.Run("error on negative capacity", func(t *testing.T) {
		t.Parallel()
		_, err := NewRingBuffer[int](-1)
		if err != ErrInvalidCapacity {
			t.Errorf("got error %v, want ErrInvalidCapacity", err)
		}
	})
}

func TestRingBuffer_Empty(t *testing.T) {
	t.Parallel()

	buf := mustNewRingBuffer[int](5)

	t.Run("size is zero", func(t *testing.T) {
		if buf.Size() != 0 {
			t.Errorf("got size %d, want 0", buf.Size())
		}
	})

	t.Run("latest returns false", func(t *testing.T) {
		_, ok := buf.Latest()
		if ok {
			t.Error("Latest() should return false for empty buffer")
		}
	})

	t.Run("all returns nil", func(t *testing.T) {
		all := buf.All()
		if all != nil {
			t.Errorf("All() should return nil for empty buffer, got %v", all)
		}
	})

	t.Run("query returns nil", func(t *testing.T) {
		result := buf.Query(time.Now(), func(i int) time.Time {
			return time.Now()
		})
		if result != nil {
			t.Errorf("Query() should return nil for empty buffer, got %v", result)
		}
	})
}

func TestRingBuffer_Add(t *testing.T) {
	t.Parallel()

	t.Run("single item", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[int](5)
		buf.Add(42)

		if buf.Size() != 1 {
			t.Errorf("got size %d, want 1", buf.Size())
		}

		latest, ok := buf.Latest()
		if !ok {
			t.Fatal("Latest() returned false")
		}
		if latest != 42 {
			t.Errorf("got latest %d, want 42", latest)
		}
	})

	t.Run("multiple items", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[int](5)
		for i := 1; i <= 3; i++ {
			buf.Add(i)
		}

		if buf.Size() != 3 {
			t.Errorf("got size %d, want 3", buf.Size())
		}

		latest, _ := buf.Latest()
		if latest != 3 {
			t.Errorf("got latest %d, want 3", latest)
		}

		all := buf.All()
		expected := []int{1, 2, 3}
		if len(all) != len(expected) {
			t.Fatalf("got %d items, want %d", len(all), len(expected))
		}
		for i, v := range all {
			if v != expected[i] {
				t.Errorf("all[%d] = %d, want %d", i, v, expected[i])
			}
		}
	})

	t.Run("fill to capacity", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[int](5)
		for i := 1; i <= 5; i++ {
			buf.Add(i)
		}

		if buf.Size() != 5 {
			t.Errorf("got size %d, want 5", buf.Size())
		}

		all := buf.All()
		expected := []int{1, 2, 3, 4, 5}
		for i, v := range all {
			if v != expected[i] {
				t.Errorf("all[%d] = %d, want %d", i, v, expected[i])
			}
		}
	})
}

func TestRingBuffer_WrapAround(t *testing.T) {
	t.Parallel()

	t.Run("single wrap", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[int](5)

		// Add 7 items to a buffer of capacity 5
		for i := 1; i <= 7; i++ {
			buf.Add(i)
		}

		// Size should stay at capacity
		if buf.Size() != 5 {
			t.Errorf("got size %d, want 5", buf.Size())
		}

		// Should have items 3-7 (oldest 1,2 evicted)
		all := buf.All()
		expected := []int{3, 4, 5, 6, 7}
		if len(all) != len(expected) {
			t.Fatalf("got %d items, want %d", len(all), len(expected))
		}
		for i, v := range all {
			if v != expected[i] {
				t.Errorf("all[%d] = %d, want %d", i, v, expected[i])
			}
		}

		// Latest should be 7
		latest, _ := buf.Latest()
		if latest != 7 {
			t.Errorf("got latest %d, want 7", latest)
		}
	})

	t.Run("multiple wraps", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[int](3)

		// Add 10 items to buffer of capacity 3
		for i := 1; i <= 10; i++ {
			buf.Add(i)
		}

		// Should have items 8, 9, 10
		all := buf.All()
		expected := []int{8, 9, 10}
		for i, v := range all {
			if v != expected[i] {
				t.Errorf("all[%d] = %d, want %d", i, v, expected[i])
			}
		}
	})
}

func TestRingBuffer_Query(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2026, 1, 19, 12, 0, 0, 0, time.UTC)

	t.Run("query recent items", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[timestampedItem](10)

		// Add items at 1-second intervals
		for i := 0; i < 5; i++ {
			buf.Add(timestampedItem{
				ts:    baseTime.Add(time.Duration(i) * time.Second),
				value: i,
			})
		}

		// Query items from second 2 onwards
		since := baseTime.Add(2 * time.Second)
		result := buf.Query(since, func(item timestampedItem) time.Time {
			return item.ts
		})

		if len(result) != 3 {
			t.Fatalf("got %d items, want 3", len(result))
		}

		for i, item := range result {
			expected := i + 2
			if item.value != expected {
				t.Errorf("result[%d].value = %d, want %d", i, item.value, expected)
			}
		}
	})

	t.Run("query all items", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[timestampedItem](10)

		for i := 0; i < 5; i++ {
			buf.Add(timestampedItem{
				ts:    baseTime.Add(time.Duration(i) * time.Second),
				value: i,
			})
		}

		// Query from before first item
		since := baseTime.Add(-1 * time.Second)
		result := buf.Query(since, func(item timestampedItem) time.Time {
			return item.ts
		})

		if len(result) != 5 {
			t.Fatalf("got %d items, want 5", len(result))
		}
	})

	t.Run("query no items", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[timestampedItem](10)

		for i := 0; i < 5; i++ {
			buf.Add(timestampedItem{
				ts:    baseTime.Add(time.Duration(i) * time.Second),
				value: i,
			})
		}

		// Query from after last item
		since := baseTime.Add(10 * time.Second)
		result := buf.Query(since, func(item timestampedItem) time.Time {
			return item.ts
		})

		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("query with wraparound", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[timestampedItem](5)

		// Add 8 items, causing wrap
		for i := 0; i < 8; i++ {
			buf.Add(timestampedItem{
				ts:    baseTime.Add(time.Duration(i) * time.Second),
				value: i,
			})
		}

		// Should have items 3-7
		// Query from second 5 onwards (items 5, 6, 7)
		since := baseTime.Add(5 * time.Second)
		result := buf.Query(since, func(item timestampedItem) time.Time {
			return item.ts
		})

		if len(result) != 3 {
			t.Fatalf("got %d items, want 3", len(result))
		}

		expected := []int{5, 6, 7}
		for i, item := range result {
			if item.value != expected[i] {
				t.Errorf("result[%d].value = %d, want %d", i, item.value, expected[i])
			}
		}
	})
}

func TestRingBuffer_Latest(t *testing.T) {
	t.Parallel()

	t.Run("returns most recent", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[string](5)

		buf.Add("first")
		buf.Add("second")
		buf.Add("third")

		latest, ok := buf.Latest()
		if !ok {
			t.Fatal("Latest() returned false")
		}
		if latest != "third" {
			t.Errorf("got %q, want %q", latest, "third")
		}
	})

	t.Run("works after wraparound", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[int](3)

		for i := 1; i <= 5; i++ {
			buf.Add(i)
		}

		latest, ok := buf.Latest()
		if !ok {
			t.Fatal("Latest() returned false")
		}
		if latest != 5 {
			t.Errorf("got %d, want 5", latest)
		}
	})
}

func TestRingBuffer_All(t *testing.T) {
	t.Parallel()

	t.Run("returns chronological order", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[int](10)

		for i := 1; i <= 5; i++ {
			buf.Add(i)
		}

		all := buf.All()
		for i, v := range all {
			expected := i + 1
			if v != expected {
				t.Errorf("all[%d] = %d, want %d", i, v, expected)
			}
		}
	})

	t.Run("returns chronological after wrap", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[int](4)

		for i := 1; i <= 6; i++ {
			buf.Add(i)
		}

		all := buf.All()
		expected := []int{3, 4, 5, 6}
		for i, v := range all {
			if v != expected[i] {
				t.Errorf("all[%d] = %d, want %d", i, v, expected[i])
			}
		}
	})
}

func TestRingBuffer_FindNearest(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2026, 1, 19, 12, 0, 0, 0, time.UTC)

	t.Run("finds exact match", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[timestampedItem](10)

		for i := 0; i < 5; i++ {
			buf.Add(timestampedItem{
				ts:    baseTime.Add(time.Duration(i) * time.Second),
				value: i,
			})
		}

		target := baseTime.Add(2 * time.Second)
		result, ok := buf.FindNearest(target, func(item timestampedItem) time.Time {
			return item.ts
		})

		if !ok {
			t.Fatal("FindNearest returned false")
		}
		if result.value != 2 {
			t.Errorf("got value %d, want 2", result.value)
		}
	})

	t.Run("finds closest when between items", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[timestampedItem](10)

		for i := 0; i < 5; i++ {
			buf.Add(timestampedItem{
				ts:    baseTime.Add(time.Duration(i*10) * time.Second),
				value: i,
			})
		}

		// Target is 25 seconds, should find item at 20 (value=2) or 30 (value=3)
		target := baseTime.Add(25 * time.Second)
		result, ok := buf.FindNearest(target, func(item timestampedItem) time.Time {
			return item.ts
		})

		if !ok {
			t.Fatal("FindNearest returned false")
		}
		// Either 2 or 3 is acceptable (both are 5 seconds away)
		if result.value != 2 && result.value != 3 {
			t.Errorf("got value %d, want 2 or 3", result.value)
		}
	})

	t.Run("empty buffer returns false", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[timestampedItem](10)

		_, ok := buf.FindNearest(time.Now(), func(item timestampedItem) time.Time {
			return item.ts
		})

		if ok {
			t.Error("FindNearest should return false for empty buffer")
		}
	})
}

func TestRingBuffer_QueryFunc(t *testing.T) {
	t.Parallel()

	t.Run("filters items", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[int](10)

		for i := 1; i <= 10; i++ {
			buf.Add(i)
		}

		// Get even numbers
		result := buf.QueryFunc(func(i int) bool {
			return i%2 == 0
		})

		expected := []int{2, 4, 6, 8, 10}
		if len(result) != len(expected) {
			t.Fatalf("got %d items, want %d", len(result), len(expected))
		}
		for i, v := range result {
			if v != expected[i] {
				t.Errorf("result[%d] = %d, want %d", i, v, expected[i])
			}
		}
	})

	t.Run("no matches returns nil", func(t *testing.T) {
		t.Parallel()
		buf := mustNewRingBuffer[int](10)

		for i := 1; i <= 5; i++ {
			buf.Add(i)
		}

		result := buf.QueryFunc(func(i int) bool {
			return i > 100
		})

		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})
}

func TestRingBuffer_Clear(t *testing.T) {
	t.Parallel()

	buf := mustNewRingBuffer[int](5)

	for i := 1; i <= 5; i++ {
		buf.Add(i)
	}

	buf.Clear()

	if buf.Size() != 0 {
		t.Errorf("got size %d, want 0", buf.Size())
	}

	_, ok := buf.Latest()
	if ok {
		t.Error("Latest() should return false after Clear()")
	}

	all := buf.All()
	if all != nil {
		t.Errorf("All() should return nil after Clear(), got %v", all)
	}

	// Verify we can add items again
	buf.Add(100)
	if buf.Size() != 1 {
		t.Errorf("got size %d, want 1 after re-adding", buf.Size())
	}
	latest, _ := buf.Latest()
	if latest != 100 {
		t.Errorf("got latest %d, want 100", latest)
	}
}

func TestRingBuffer_Concurrent(t *testing.T) {
	t.Parallel()

	buf := mustNewRingBuffer[int](100)
	var wg sync.WaitGroup

	// Concurrent writers
	for w := 0; w < 10; w++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				buf.Add(writerID*1000 + i)
			}
		}(w)
	}

	// Concurrent readers
	for r := 0; r < 5; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = buf.Size()
				_, _ = buf.Latest()
				_ = buf.All()
			}
		}()
	}

	wg.Wait()

	// Verify buffer is in valid state
	if buf.Size() != 100 {
		t.Errorf("got size %d, want 100 (capacity)", buf.Size())
	}

	all := buf.All()
	if len(all) != 100 {
		t.Errorf("got %d items from All(), want 100", len(all))
	}
}

func TestRingBuffer_ConcurrentReadWrite(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2026, 1, 19, 12, 0, 0, 0, time.UTC)
	buf := mustNewRingBuffer[timestampedItem](50)

	var wg sync.WaitGroup

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			buf.Add(timestampedItem{
				ts:    baseTime.Add(time.Duration(i) * time.Millisecond),
				value: i,
			})
			time.Sleep(time.Microsecond)
		}
	}()

	// Reader goroutines doing queries
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = buf.Query(baseTime, func(item timestampedItem) time.Time {
					return item.ts
				})
				_, _ = buf.FindNearest(time.Now(), func(item timestampedItem) time.Time {
					return item.ts
				})
			}
		}()
	}

	wg.Wait()

	// Just verify no panic/race occurred
	if buf.Size() == 0 {
		t.Error("buffer should have items after concurrent operations")
	}
}

// Benchmark tests

func BenchmarkRingBuffer_Add(b *testing.B) {
	buf := mustNewRingBuffer[int](1000)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		buf.Add(i)
	}
}

func BenchmarkRingBuffer_Latest(b *testing.B) {
	buf := mustNewRingBuffer[int](1000)
	for i := 0; i < 1000; i++ {
		buf.Add(i)
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = buf.Latest()
	}
}

func BenchmarkRingBuffer_All(b *testing.B) {
	buf := mustNewRingBuffer[int](1000)
	for i := 0; i < 1000; i++ {
		buf.Add(i)
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = buf.All()
	}
}

func BenchmarkRingBuffer_Query(b *testing.B) {
	baseTime := time.Now()
	buf := mustNewRingBuffer[timestampedItem](1000)
	for i := 0; i < 1000; i++ {
		buf.Add(timestampedItem{
			ts:    baseTime.Add(time.Duration(i) * time.Second),
			value: i,
		})
	}

	since := baseTime.Add(500 * time.Second)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = buf.Query(since, func(item timestampedItem) time.Time {
			return item.ts
		})
	}
}
