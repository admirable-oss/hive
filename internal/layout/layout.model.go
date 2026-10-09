package layout

import (
	"errors"
	"fmt"
)

// Orientation is how a split divides its area.
type Orientation string

const (
	// Horizontal places the children side by side (a vertical border).
	Horizontal Orientation = "horizontal"
	// Vertical stacks the children (a horizontal border).
	Vertical Orientation = "vertical"
)

// Direction is where something goes relative to a pane.
type Direction string

const (
	Left  Direction = "left"
	Right Direction = "right"
	Up    Direction = "up"
	Down  Direction = "down"
)

// ParseDirection accepts left, right, up and down.
func ParseDirection(s string) (Direction, error) {
	switch d := Direction(s); d {
	case Left, Right, Up, Down:
		return d, nil
	}
	return "", fmt.Errorf("%w: direction %q (want left, right, up or down)", ErrInvalid, s)
}

func (d Direction) orientation() Orientation {
	if d == Left || d == Right {
		return Horizontal
	}
	return Vertical
}

// forward reports whether d points to the second child of a split.
func (d Direction) forward() bool { return d == Right || d == Down }

// Node is a leaf (Pane set) or a split (Split set, with First and Second).
type Node struct {
	Pane   string      `json:"pane,omitempty"`
	Split  Orientation `json:"split,omitempty"`
	Ratio  float64     `json:"ratio,omitempty"`
	First  *Node       `json:"first,omitempty"`
	Second *Node       `json:"second,omitempty"`
}

// Leaf returns a node holding one pane.
func Leaf(pane string) *Node { return &Node{Pane: pane} }

// IsLeaf reports whether n holds a pane.
func (n *Node) IsLeaf() bool { return n != nil && n.Pane != "" }

// Rect is an area of terminal cells.
type Rect struct {
	X, Y, W, H int
}

// Ratio limits keep both children of a split visible.
const (
	MinRatio     = 0.05
	MaxRatio     = 0.95
	DefaultRatio = 0.5
)

var (
	ErrInvalid  = errors.New("layout: invalid")
	ErrNotFound = errors.New("layout: pane not found")
	ErrNoBorder = errors.New("layout: no border in that direction")
)
