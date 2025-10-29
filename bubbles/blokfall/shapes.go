package blokfall

import (
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"strings"
)

func RandShape() string {
	return ShapeKeys[rand.Intn(len(ShapeKeys))]
}

type Point struct {
	X, Y int
}

var ShapeRange struct {
	Min, Max Point
}

var ShapeKeys = slices.Sorted(maps.Keys(VisualShapes))

var VisualShapes map[string][]Point

func init() {
	visualDefs := map[string]string{
		"straight4": `
|OXOO
`,
		// "straight5": `
		// |OOXOO
		// `,
		"box": `
|XO
|OO
`,
		// "boxbig": `
		// |OOO
		// |OXO
		// |OOO
		// `,
		"halfx": `
|OXO
| O
`,
		"bend-right": `
|OXO
|  O
`,
		"bend-left": `
|OXO
|O
`,
		"box-zigr": `
| XO
|OO
`,
		"box-zigl": `
|OX
| OO
`,
		"Q": `
|OXO
|OO
`,
	}

	VisualShapes = make(map[string][]Point)
	for k, v := range visualDefs {
		p, err := parseVisual(v)
		if err != nil {
			panic(fmt.Sprintf("failed to parse visual for %s: %v", k, err))
		}
		VisualShapes[k] = p
	}

	var minX, maxX, minY, maxY int
	for _, blks := range VisualShapes {
		for _, b := range blks {
			minX = min(minX, b.X)
			maxX = max(maxX, b.X)
			minY = min(minY, b.Y)
			maxY = max(maxY, b.Y)
		}
	}

	ShapeRange.Min.X = minX
	ShapeRange.Max.X = maxX
	ShapeRange.Min.Y = minY
	ShapeRange.Max.Y = maxY

	ShapeKeys = slices.Sorted(maps.Keys(VisualShapes))
}

// parseVisual converts a visual raw string into []Point. It expects lines that
// begin with '|' characters after '|' are the grid columns. The position of
// 'X' becomes (0,0) — other blocks are returned relative to that origin. Y
// increases downward.
func parseVisual(v string) ([]Point, error) {
	v = strings.TrimSpace(v)
	lines := make([]string, 0, 4)
	for ln := range strings.SplitSeq(v, "\n") {
		if ln == "" {
			continue
		}
		if !strings.HasPrefix(ln, "|") {
			continue
		}
		lines = append(lines, ln[1:]) // drop the '|' border char
	}

	// find origin
	var originY, originX = -10, -10
	for y, row := range lines {
		for x, ch := range row {
			if ch == 'X' {
				originY, originX = y, x
				break
			}
		}
		if originY != -10 {
			break
		}
	}
	if originY == -10 {
		return nil, fmt.Errorf("no origin 'X' found")
	}

	var pts []Point
	for y, row := range lines {
		for x, ch := range row {
			if ch == 'O' || ch == 'X' {
				pts = append(pts, Point{X: x - originX, Y: y - originY})
			}
		}
	}
	return pts, nil
}
