package unsafering

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuffer(t *testing.T) {
	r := New[int](5)

	for i := range 7 {
		r.Push(i)
	}

	s := slices.Collect(r.Iter())
	require.Equal(t, []int{2, 3, 4, 5, 6}, s)

	s = slices.Collect(r.IterRecent(2))
	require.Equal(t, []int{5, 6}, s)

	v, _ := r.AtInWindow(0, 3)
	require.Equal(t, 4, v)

	v, _ = r.AtInWindow(0, 1)
	require.Equal(t, 6, v)

	v, ok := r.AtInWindow(0, 0)
	assert.False(t, ok)
	require.Equal(t, 0, v)
}
