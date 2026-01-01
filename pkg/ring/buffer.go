package ring

import (
	"sync"
)

type Buffer[T any] struct {
	buffer []T
	size   int
	mu     sync.RWMutex // Use RWMutex for better concurrent read performance
	write  int
	count  int
}

func New[T any](size int) *Buffer[T] {
	return &Buffer[T]{
		buffer: make([]T, size),
		size:   size,
	}
}

// Add inserts a new element into the buffer, overwriting the oldest if full.
func (rb *Buffer[T]) Add(value T) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.buffer[rb.write] = value
	rb.write = (rb.write + 1) % rb.size

	if rb.count < rb.size {
		rb.count++
	}
}

// Get returns the contents of the buffer in FIFO order using efficient block copying.
func (rb *Buffer[T]) Get() []T {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.count == 0 {
		return nil
	}

	result := make([]T, rb.count)

	if rb.count < rb.size {
		// Buffer is not yet full, data is contiguous from index 0
		copy(result, rb.buffer[:rb.count])
	} else {
		// Buffer is full, data is split around the write pointer
		// Part 1: From write pointer to end (older data)
		n := copy(result, rb.buffer[rb.write:])
		// Part 2: From start to write a pointer (newer data)
		copy(result[n:], rb.buffer[:rb.write])
	}

	return result
}

// Len returns the current number of elements in the buffer.
func (rb *Buffer[T]) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count
}
