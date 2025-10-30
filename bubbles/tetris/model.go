package tetris

import (
	"fmt"
	"io"
	"maps"
	"math"
	"math/rand"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func New() *Model {
	return &Model{}
}

type Model struct {
	b strings.Builder

	piece *Piece
	next  any

	board *Board

	render bool
}

func (m Model) Height() int {
	return m.board.Height
}

var _ tea.Model = &Model{}

const DefaultTick = time.Millisecond * 800

type TickMsg time.Time

func NewTick(d time.Duration) tea.Cmd {
	return tea.Tick(d, newTickMsg)
}

func newTickMsg(t time.Time) tea.Msg { return TickMsg(t) }

type LockMsg time.Time

func NewLock(d time.Duration) tea.Cmd {
	return tea.Tick(d, newLockMsg)
}

func newLockMsg(t time.Time) tea.Msg { return LockMsg(t) }

func (m *Model) Init() tea.Cmd {
	m.piece = NewPiece("T", 4, 0)
	m.board = NewBoard(10, 20)
	m.render = true

	return NewTick(DefaultTick)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m.UpdateTetris(msg)
}

type Input string

const (
	InputNone    Input = ""
	RotateCCWMsg Input = "j"
	RotateCWMsg  Input = "k"
	LeftMsg      Input = "d"
	RightMsg     Input = "f"
	HardDownMsg  Input = " "
	SoftDownMsg  Input = "enter"
)

var InputRune = map[Input]rune{
	InputNone:    ' ',
	RotateCCWMsg: '↶',
	RotateCWMsg:  '↷',
	LeftMsg:      '←',
	RightMsg:     '→',
	HardDownMsg:  '⤓',
}

func (m *Model) UpdateTetris(msg tea.Msg) (*Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.HandleInput(Input(msg.String()))
	case Input:
		return m.HandleInput(msg)

	case TickMsg:
		return m, m.HandleTickMsg(msg)
	case LockMsg:
		return m, m.HandleLockMsg(msg)
	}
	return m, nil
}

func (m *Model) UpdateTetrisShouldRender(msg tea.Msg) (*Model, tea.Cmd, bool) {
	var cmd tea.Cmd
	m, cmd = m.UpdateTetris(msg)
	return m, cmd, m.render
}

func (m *Model) HandleInput(msg Input) (*Model, tea.Cmd) {
	switch msg {
	case RotateCCWMsg:
		m.RotateCCW()
	case RotateCWMsg:
		m.RotateCW()
	case LeftMsg:
		m.Left()
	case RightMsg:
		m.Right()
	case HardDownMsg:
		return m, m.HardDown()
	case SoftDownMsg:
		// TODO: soft down
	}

	return m, nil
}

func (m *Model) RotateCW() {
	RotateCW(m.piece)
	if m.board.Collides(m.piece) {
		RotateCCW(m.piece)
	} else {
		m.render = true
	}
}

func (m *Model) RotateCCW() {
	RotateCCW(m.piece)
	if m.board.Collides(m.piece) {
		RotateCW(m.piece)
	} else {
		m.render = true
	}
}

func (m *Model) Left() {
	m.piece.X--
	if m.board.Collides(m.piece) {
		m.piece.X++
	} else {
		m.render = true
	}
}

func (m *Model) Right() {
	m.piece.X++
	if m.board.Collides(m.piece) {
		m.piece.X--
	} else {
		m.render = true
	}
}

func (m *Model) HardDown() tea.Cmd {
	for !m.board.Collides(m.piece) {
		m.piece.Y++
	}
	m.piece.Y--
	m.render = true
	return m.LockPiece()
}

// TODO: return a new Tick that will invalidate the existing one
// func (m *Model) LockPiece(resetTick bool) tea.Cmd {
func (m *Model) LockPiece() tea.Cmd {
	m.board.LockPiece(m.piece)
	m.piece = NewPiece(RandShape(), 4, 0)
	return nil
}

func (m *Model) HandleTickMsg(TickMsg) tea.Cmd {
	m.piece.Y++
	if m.board.Collides(m.piece) {
		m.piece.Y--
		return NewLock(DefaultTick)
	}
	m.render = true
	return NewTick(DefaultTick)
}

func (m *Model) HandleLockMsg(msg LockMsg) tea.Cmd {
	m.piece.Y++
	if m.board.Collides(m.piece) {
		m.piece.Y--
		m.LockPiece()
	}
	m.render = true
	return NewTick(DefaultTick)
}

func (m *Model) View() string {
	if !m.render {
		return m.b.String()
	}

	m.b.Reset()
	m.board.Print(&m.b, m.piece)

	m.render = false
	return m.b.String()
}

const (
	Empty = 0
)

type Point struct {
	X, Y int
}

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
}

func NewBoard(w, h int) *Board {
	cells := make([][]uint8, h)
	for i := range cells {
		cells[i] = make([]uint8, w)
	}

	colors := make(map[uint8]lipgloss.Style, math.MaxUint8)
	for i := range math.MaxUint8 {
		colors[uint8(i)] = lipgloss.NewStyle().Background(lipgloss.ANSIColor(i))
	}
	return &Board{
		Width: w, Height: h,
		cleared: make([][]uint8, 4),
		lines:   make([][]uint8, h),
		Cells:   cells,
		Colors:  colors,
	}
}

func (b *Board) Print(w io.Writer, p *Piece) {
	for y := 0; y < b.Height; y++ {
		for x := 0; x < b.Width; x++ {
			cell := b.Cells[y][x]

			// overlay active piece
			if p != nil {
				for _, blk := range p.Blocks {
					bx := p.X + blk.X
					by := p.Y + blk.Y
					if bx == x && by == y {
						cell = p.Color
					}
				}
			}

			if cell == Empty {
				fmt.Fprint(w, "  ")
			} else {
				fmt.Fprint(w, b.Colors[cell].Render("  "))
			}
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w)
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
		b.X, b.Y = b.Y, -b.X
	}
}

func RotateCCW(p *Piece) {
	for i := range p.Blocks {
		b := &p.Blocks[i]
		b.X, b.Y = -b.Y, b.X
	}
}

var Shapes = map[string][]Point{
	"I": {{-1, 0}, {0, 0}, {1, 0}, {2, 0}},
	"O": {{0, 0}, {1, 0}, {0, 1}, {1, 1}},
	"T": {{-1, 0}, {0, 0}, {1, 0}, {0, 1}},
	"L": {{-1, 0}, {0, 0}, {1, 0}, {1, 1}},
	"J": {{-1, 1}, {-1, 0}, {0, 0}, {1, 0}},
	"S": {{0, 0}, {1, 0}, {-1, 1}, {0, 1}},
	"Z": {{-1, 0}, {0, 0}, {0, 1}, {1, 1}},
}

var ShapeKeys = slices.Sorted(maps.Keys(Shapes))

func RandShape() string {
	return ShapeKeys[rand.Intn(len(ShapeKeys))]
}

func NewPiece(kind string, x, y int) *Piece {
	blocks := make([]Point, len(Shapes[kind]))
	copy(blocks, Shapes[kind])

	return &Piece{
		Kind:   kind,
		Blocks: blocks,
		X:      x, Y: y,
		Color: uint8(rand.Intn(math.MaxUint8)),
	}
}
