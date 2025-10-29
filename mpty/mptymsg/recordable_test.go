package mptymsg

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type exampleMsg struct {
	At    time.Time
	Value string
}

var _ Recordable = exampleMsg{}

func (m exampleMsg) TypeName() string {
	return fmt.Sprintf("%T", m)
}

func (m exampleMsg) Ts() time.Time {
	return m.At
}

func (m exampleMsg) SetId(int64) Recordable {
	return m
}

func init() {
	Register(exampleMsg{})
}

func TestRecordable(t *testing.T) {
	data, err := JsonMarshal(exampleMsg{
		At:    time.Unix(1, 0),
		Value: "testing",
	})
	require.NoError(t, err)
	t.Log(string(data))

	got, err := JsonUnmarshal(data)
	require.NoError(t, err)
	require.IsType(t, got, exampleMsg{})

	gotT := got.(exampleMsg)
	require.Equal(t,
		time.Unix(1, 0).Format(time.RFC3339Nano),
		gotT.At.Format(time.RFC3339Nano),
	)
	require.Equal(t, "testing", gotT.Value)
}
