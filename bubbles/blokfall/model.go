package blokfall

import (
	"fmt"
	"io"
	"math"
	"math/rand"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/ghthor/webtea/unsafering"
)

type GameResetMsg int
type ToggleDebugMsg int
type SetLevelMsg int

const (
	DebugBlock   = "╺╸"
	DefaultBlock = "  "
	DefaultEmpty = "  "
)

func New() *Model {
	return &Model{}
}

type tableView struct {
	board     string
	nextPiece string
}

var _ table.Data = tableView{}

func (t tableView) At(row, col int) string {
	switch col {
	case 0:
		return t.board
	case 1:
		return t.nextPiece
	default:
		return ""
	}
}

func (t tableView) Rows() int    { return 1 }
func (t tableView) Columns() int { return 2 }

type Model struct {
	b        strings.Builder
	pieceBuf strings.Builder

	next   *unsafering.Buffer[*Piece]
	pieces []*Piece
	ticks  []int64

	board *Board

	render bool

	table *table.Table
	tableView

	level       int
	linesScored int
	score       uint64

	debug bool
}

func (m Model) Height() int {
	return m.board.Height
}

var _ tea.Model = &Model{}

type TickMsg struct {
	time.Time
	Idx  int
	Tick int64
}

func NewTick(d time.Duration, i int, tick int64) tea.Cmd {
	return tea.Tick(d, newTickMsg(i, tick))
}

func newTickMsg(i int, tick int64) func(time.Time) tea.Msg {
	return func(t time.Time) tea.Msg { return TickMsg{t, i, tick} }
}

type LockMsg struct {
	time.Time
	Idx  int
	Tick int64
}

func NewLock(d time.Duration, i int, tick int64) tea.Cmd {
	return tea.Tick(d, newLockMsg(i, tick))
}

func newLockMsg(i int, tick int64) func(time.Time) tea.Msg {
	return func(t time.Time) tea.Msg { return LockMsg{t, i, tick} }
}

func (m *Model) Init() tea.Cmd {
	m.pieces = make([]*Piece, 0, 4)
	m.ticks = make([]int64, 0, 4)
	m.board = NewBoard(12, 24)
	m.table = table.New().Border(lipgloss.RoundedBorder())
	m.render = true
	return m.Reset(0)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m.UpdateBlokFall(msg)
}

type Input string

type MultiPieceInput struct {
	Input
	Idx int
}

const (
	InputNone    Input = ""
	RotateCCWMsg Input = "j"
	RotateCWMsg  Input = "k"
	LeftMsg      Input = "d"
	RightMsg     Input = "f"
	HardDownMsg  Input = " "
	SoftDownMsg  Input = "g"
)

var InputRune = map[Input]rune{
	InputNone:    ' ',
	RotateCCWMsg: '↶',
	RotateCWMsg:  '↷',
	LeftMsg:      '←',
	RightMsg:     '→',
	HardDownMsg:  '⤓',
	SoftDownMsg:  '↓',
}

func (m *Model) UpdateBlokFall(msg tea.Msg) (*Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.HandleInput(MultiPieceInput{Input: Input(msg.String())})
	case Input:
		return m.HandleInput(MultiPieceInput{Input: msg})
	case MultiPieceInput:
		return m.HandleInput(msg)

	case GameResetMsg:
		m.render = true
		return m, m.Reset(0)

	case SetLevelMsg:
		m.render = true
		return m, m.Reset(int(msg))

	case ToggleDebugMsg:
		m.debug = !m.debug
		if m.debug {
			m.board.Filled = DebugBlock
		} else {
			m.board.Filled = DefaultBlock
		}
		m.render = true

	case TickMsg:
		return m, m.HandleTickMsg(msg)
	case LockMsg:
		return m, m.HandleLockMsg(msg)
	}
	return m, nil
}

func (m *Model) UpdateBlokFallShouldRender(msg tea.Msg) (*Model, tea.Cmd, bool) {
	var cmd tea.Cmd
	m, cmd = m.UpdateBlokFall(msg)
	return m, cmd, m.render
}

func (m *Model) HandleInput(msg MultiPieceInput) (*Model, tea.Cmd) {
	if msg.Idx >= len(m.pieces) {
		return m, nil
	}

	switch msg.Input {
	case RotateCCWMsg:
		m.RotateCCW(msg.Idx)

	case RotateCWMsg:
		m.RotateCW(msg.Idx)

	case LeftMsg:
		m.Left(msg.Idx)

	case RightMsg:
		m.Right(msg.Idx)

	case HardDownMsg:
		return m, m.HardDown(msg.Idx)

	case SoftDownMsg:
		return m, m.SoftDown(msg.Idx)
	}

	return m, nil
}

func (m *Model) RotateCW(i int) {
	p := m.pieces[i]

	RotateCW(p)
	if m.board.Collides(p) {
		RotateCCW(p)
	} else {
		m.render = true
	}
}

func (m *Model) RotateCCW(i int) {
	p := m.pieces[i]

	RotateCCW(p)
	if m.board.Collides(p) {
		RotateCW(p)
	} else {
		m.render = true
	}
}

func (m *Model) Left(i int) {
	p := m.pieces[i]

	p.X--
	if m.board.Collides(p) {
		p.X++
	} else {
		m.render = true
	}
}

func (m *Model) Right(i int) {
	p := m.pieces[i]

	p.X++
	if m.board.Collides(p) {
		p.X--
	} else {
		m.render = true
	}
}

func (m *Model) HardDown(i int) tea.Cmd {
	p := m.pieces[i]

	for !m.board.Collides(p) {
		p.Y++
	}
	p.Y--
	return m.LockPiece(i)
}

func (m *Model) SoftDown(i int) tea.Cmd {
	p := m.pieces[i]

	if m.board.Collides(p) {
		return m.LockPiece(i)
	}

	p.Y++
	if m.board.Collides(p) {
		p.Y--
		return m.LockPiece(i)
	}
	m.render = true
	return m.NewTick(i)
}

// TODO: return a new Tick that will invalidate the existing one
// func (m *Model) LockPiece(resetTick bool) tea.Cmd {
func (m *Model) LockPiece(i int) tea.Cmd {
	p := m.pieces[i]
	m.board.LockPiece(p)
	m.pieces[i] = m.PullNext()
	m.render = true
	return m.NewTick(i)
}

func (m *Model) HandleTickMsg(msg TickMsg) tea.Cmd {
	i := msg.Idx
	if i >= len(m.pieces) {
		return nil
	}

	if msg.Tick != m.ticks[i] {
		// Tick was canceled
		return nil
	}

	p := m.pieces[i]
	if p == nil {
		return nil
	}

	p.Y++
	if m.board.Collides(p) {
		p.Y--
		return m.NewLock(i)
	}
	m.render = true
	return m.NewTick(i)
}

func (m *Model) HandleLockMsg(msg LockMsg) tea.Cmd {
	i := msg.Idx
	if i >= len(m.pieces) {
		return nil
	}

	if msg.Tick != m.ticks[i] {
		// Tick was canceled
		return nil
	}

	p := m.pieces[i]
	if p == nil {
		return nil
	}

	p.Y++
	if m.board.Collides(p) {
		p.Y--
		return m.LockPiece(i)
	}
	m.render = true
	return m.NewTick(i)
}

func (m *Model) NewTick(i int) tea.Cmd {
	tick := m.ticks[i]
	tick++
	m.ticks[i] = tick
	return NewTick(GravityByLevel(m.level), i, tick)
}

func (m *Model) NewLock(i int) tea.Cmd {
	tick := m.ticks[i]
	tick++
	m.ticks[i] = tick
	return NewLock(GravityByLevel(m.level), i, tick)
}

func (m *Model) View() string {
	if !m.render {
		return m.b.String()
	}

	m.b.Reset()
	m.board.Print(&m.b, m.pieces)

	m.tableView.board = m.b.String()
	m.b.Reset()

	m.ViewNextPiecesIn(&m.b)
	m.tableView.nextPiece = m.b.String()
	m.b.Reset()

	m.render = false
	m.table.Data(m.tableView)
	m.b.WriteString(m.table.Render())
	return m.b.String()
}

func (m *Model) ViewNextPiecesIn(w io.Writer) {
	for p := range m.next.Iter() {
		m.PrintPiece(w, p)
	}
}

const (
	Empty = 0
)

type Piece struct {
	Kind   string
	Blocks []Point // relative coordinates
	X, Y   int     // position on board
	Color  uint8
}

type Board struct {
	Width, Height int

	cleared [][]uint8
	lines   [][]uint8
	Cells   [][]uint8

	Colors map[uint8]lipgloss.Style

	Filled string
}

const (
	// https://github.com/fidian/ansi?tab=readme-ov-file#--color-codes
	colorMin   = 17
	colorMax   = 231
	colorRange = colorMax + 1 - colorMin // [17, 231]
)

func RandColor() uint8 {
	n := uint8(rand.Intn(colorRange))
	return n + colorMin
}

func NewBoard(w, h int) *Board {
	cells := make([][]uint8, h)
	for i := range cells {
		cells[i] = make([]uint8, w)
	}

	colors := make(map[uint8]lipgloss.Style, math.MaxUint8)

	for i := range colorRange {
		i += colorMin
		colors[uint8(i)] = lipgloss.NewStyle().Background(lipgloss.ANSIColor(i))
	}
	return &Board{
		Width: w, Height: h,
		cleared: make([][]uint8, 6),
		lines:   make([][]uint8, h),
		Cells:   cells,
		Colors:  colors,
		Filled:  DefaultBlock,
	}
}

func (b *Board) Print(w io.Writer, pieces []*Piece) {
	filled := b.Filled

	for y := 0; y < b.Height; y++ {
		for x := 0; x < b.Width; x++ {
			cell := b.Cells[y][x]

			// overlay active pieces
		overlay:
			for _, p := range pieces {
				if p == nil {
					continue
				}
				for _, blk := range p.Blocks {
					bx := p.X + blk.X
					by := p.Y + blk.Y
					if bx == x && by == y {
						cell = p.Color
						break overlay
					}
				}
			}

			if cell == Empty {
				fmt.Fprint(w, DefaultEmpty)
			} else {
				fmt.Fprint(w, b.Colors[cell].Render(filled))
			}
		}
		if y+1 != b.Height {
			fmt.Fprintln(w)
		}
	}
}

func (m *Model) PrintPiece(w io.Writer, p *Piece) {
	// TODO: calculate these y,x start end ranges
	b := &m.pieceBuf
	b.Reset()

	for y := ShapeRange.Min.Y; y <= ShapeRange.Max.Y; y++ {
		for x := ShapeRange.Min.X; x <= ShapeRange.Max.X; x++ {
			cell := uint8(Empty)
			for _, blk := range p.Blocks {
				if blk.X == x && blk.Y == y {
					cell = p.Color
					break
				}
			}
			if cell == Empty {
				fmt.Fprint(b, DefaultEmpty)
			} else {
				fmt.Fprint(b, m.board.Colors[cell].Render(m.board.Filled))
			}
		}
		if y < ShapeRange.Max.Y {
			fmt.Fprintln(b)
		}
	}
	fmt.Fprintln(w, b.String())
}

func (b *Board) Collides(p *Piece) bool {
	for _, blk := range p.Blocks {
		bx := p.X + blk.X
		by := p.Y + blk.Y
		if bx < 0 || bx >= b.Width || by >= b.Height {
			return true
		}
		if by >= 0 && b.Cells[by][bx] != Empty {
			return true
		}
	}
	return false
}

func (b *Board) LockPiece(p *Piece) {
	for _, blk := range p.Blocks {
		bx := p.X + blk.X
		by := p.Y + blk.Y
		if by >= 0 && by < b.Height && bx >= 0 && bx < b.Width {
			b.Cells[by][bx] = p.Color
		}
	}
	// TODO: score based on lines cleared
	b.ClearLines()
}

func (b *Board) ClearLines() int {
	b.lines, b.Cells = b.Cells, b.lines
	b.Cells = b.Cells[:0]

	b.cleared = b.cleared[:0]
	cleared := 0

	// iterate from bottom to top
	for y := b.Height - 1; y >= 0; y-- {
		full := true
		for x := 0; x < b.Width; x++ {
			if b.lines[y][x] == Empty {
				full = false
				break
			}
		}
		if full {
			cleared++
			b.cleared = append(b.cleared, b.lines[y])
		} else {
			// keep this line
			b.Cells = append(b.Cells, b.lines[y])
		}
	}

	// Empty the cleared lines and reappend them to the top of the board
	for y := range b.cleared {
		for x := range b.cleared[y] {
			b.cleared[y][x] = Empty
		}
		b.Cells = append(b.Cells, b.cleared[y])
	}

	// add new empty rows at top
	for len(b.Cells) < b.Height {
		b.Cells = append(b.Cells, make([]uint8, b.Width))
	}
	// reverse since we built from bottom up
	slices.Reverse(b.Cells)

	return cleared
}

func RotateCW(p *Piece) {
	for i := range p.Blocks {
		b := &p.Blocks[i]
		b.X, b.Y = -b.Y, b.X
	}
}

func RotateCCW(p *Piece) {
	for i := range p.Blocks {
		b := &p.Blocks[i]
		b.X, b.Y = b.Y, -b.X
	}
}

func NewPiece(kind string, x, y int) *Piece {
	blocks := make([]Point, len(VisualShapes[kind]))
	copy(blocks, VisualShapes[kind])

	return &Piece{
		Kind:   kind,
		Blocks: blocks,
		X:      x, Y: y,
		Color: RandColor(),
	}
}

func (m *Model) PullNext() *Piece {
	next, _ := m.next.AtInWindow(0, m.next.Len())
	if next == nil {
		next = m.newRandPiece()
	}
	m.next.Push(m.newRandPiece())
	m.render = true
	return next
}

func (m *Model) InsertNewPiece() (int, tea.Cmd) {
	next := m.PullNext()

	for i, p := range m.pieces {
		if p == nil {
			m.pieces[i] = next
			m.ticks[i] = 0
			return i, m.NewTick(i)
		}
	}

	i := len(m.pieces)
	m.pieces = append(m.pieces, next)
	m.ticks = append(m.ticks, 0)
	return i, m.NewTick(i)
}

func (m *Model) RemovePiece(i int) {
	if i >= len(m.pieces) {
		return
	}

	m.pieces[i] = nil
}

func (b *Board) Reset() {
	for y := range b.Cells {
		for x := range b.Cells[y] {
			b.Cells[y][x] = Empty
		}
	}
}

func (m *Model) Reset(lv int) tea.Cmd {
	m.board.Reset()
	m.next = unsafering.New[*Piece](3)
	for m.next.Len() < 3 {
		m.next.Push(m.newRandPiece())
	}

	cmds := make([]tea.Cmd, 0, len(m.pieces))
	for i, p := range m.pieces {
		m.ticks[i] = 0
		if p == nil {
			continue
		}

		m.pieces[i] = m.PullNext()
		cmds = append(cmds, m.NewTick(i))
	}
	m.level = lv
	m.linesScored = 0
	m.score = 0
	return tea.Batch(cmds...)
}

func (m *Model) SetLevel(i int) {
	m.level = i
}

func (m *Model) newRandPiece() *Piece {
	return NewPiece(RandShape(), m.board.Width/2, 0)
}
