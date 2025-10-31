// unsafering implements a ring buffer that has no conncurrency or parallelism
// support. It should only be used by a single process, no parallel reading and
// writing. Wrapping it in a mutex could enable that type of usecase if needed.
package unsafering

type RingBuffer[T any] struct {
	data  []T
	size  int
	count int
	write int
}

func New[T any](size int) *RingBuffer[T] {
	return &RingBuffer[T]{data: make([]T, size), size: size}
}

func (r *RingBuffer[T]) Push(v T) {
	r.data[r.write] = v
	r.write = (r.write + 1) % r.size
	r.count++
	r.count = min(r.count, r.size)
}

func (r *RingBuffer[T]) Len() int {
	if r.count < r.size {
		return r.count
	}
	return r.size
}

// ReadRecent returns the n most recent elements (oldest→newest).
// TODO: add a version of this method that can take a pre-alloced slice and
// fill it based on the cap or len
func (r *RingBuffer[T]) ReadRecent(n int) []T {
	if n > r.Len() {
		n = r.Len()
	}
	res := make([]T, n)
	start := (r.write - n + r.size) % r.size
	for i := 0; i < n; i++ {
		res[i] = r.data[(start+i)%r.size]
	}
	return res
}

// AtInWindow returns the element at index `i` within a window of
// the most recent `window` elements, in chronological order.
//
// Example:
//
//	With buffer [..., 8, 9, 10, 11, 12]
//	AtInWindow(0, 5) == 8
//	AtInWindow(4, 5) == 12
func (r *RingBuffer[T]) AtInWindow(i, window int) (val T, ok bool) {
	length := r.Len()
	if window > length {
		window = length
	}
	if i < 0 || i >= window {
		var zero T
		return zero, false
	}

	// Compute oldest element in the window:
	start := (r.write - window + r.size) % r.size
	idx := (start + i) % r.size
	return r.data[idx], true
}
