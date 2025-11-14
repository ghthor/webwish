package chat

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ghthor/webtea/mpty"
	"github.com/ghthor/webtea/mpty/mptymsg"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

const testdataDir = "testdata"

var enableGen = false

func init() {
	enableGen = os.Getenv("ENABLE_GEN") != ""
}

func TestClient(t *testing.T) {
	if enableGen {
		d := filepath.Join(testdataDir, t.Name())
		require.NoError(t, os.RemoveAll(d))
		require.NoError(t, os.MkdirAll(d, 0755))
	}

	t.Run("nick should not wrap", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		c := NewClient(ctx, &mpty.ClientInfoModel{})

		var b bytes.Buffer
		p := tea.NewProgram(c,
			tea.WithInput(nil),
			tea.WithOutput(&b),
			tea.WithContext(ctx),
		)

		grp, _ := errgroup.WithContext(ctx)
		grp.Go(func() error {
			_, err := p.Run()
			return err
		})

		p.Send(ChatSizeMsg{
			Width:  40,
			Height: 7,
		})
		p.Send([]mptymsg.Recordable{
			Msg{Str: "hi5"}.SetNick(SysNick + "12345"),
			SysMsg(time.Time{}, "system init"),
			InfoMsg(time.Time{}, "info init"),
			Msg{Str: "hi1"}.SetNick(SysNick + "1"),
			Msg{Str: "hi3"}.SetNick(SysNick + "123"),
			Msg{Str: "hi2"}.SetNick(SysNick + "12"),
		})
		p.Send(tea.KeyMsg{Type: tea.KeyCtrlC})

		p.Quit()
		require.NoError(t, grp.Wait())

		got := c.View()
		expectedFile := filepath.Join(testdataDir, t.Name())

		if enableGen {
			require.NoError(t, os.WriteFile(expectedFile, []byte(got), 0644))
		}

		expected, err := os.ReadFile(expectedFile)
		require.NoError(t, err)

		require.Equal(t, string(expected), got)
	})
}
