package layout

import (
	"fmt"
	"math"
)

// Clone returns a deep copy of n.
func (n *Node) Clone() *Node {
	if n == nil {
		return nil
	}
	c := *n
	c.First, c.Second = n.First.Clone(), n.Second.Clone()
	return &c
}

// Panes returns the panes in reading order (left to right, top to bottom).
func (n *Node) Panes() []string {
	var out []string
	var walk func(*Node)
	walk = func(n *Node) {
		switch {
		case n == nil:
		case n.IsLeaf():
			out = append(out, n.Pane)
		default:
			walk(n.First)
			walk(n.Second)
		}
	}
	walk(n)
	return out
}

// Contains reports whether pane is in the tree.
func (n *Node) Contains(pane string) bool {
	_, ok := n.path(pane)
	return ok
}

// path returns the nodes from the root to pane's leaf.
func (n *Node) path(pane string) ([]*Node, bool) {
	if n == nil {
		return nil, false
	}
	if n.IsLeaf() {
		if n.Pane == pane {
			return []*Node{n}, true
		}
		return nil, false
	}
	for _, child := range []*Node{n.First, n.Second} {
		if p, ok := child.path(pane); ok {
			return append([]*Node{n}, p...), true
		}
	}
	return nil, false
}

// Validate checks that n is well formed: every split has two children, a
// known orientation and a ratio within limits, and pane IDs are unique.
func (n *Node) Validate() error {
	seen := map[string]bool{}
	var check func(*Node) error
	check = func(n *Node) error {
		switch {
		case n == nil:
			return fmt.Errorf("%w: empty node", ErrInvalid)
		case n.Pane != "":
			if n.Split != "" || n.First != nil || n.Second != nil {
				return fmt.Errorf("%w: pane %q is both a leaf and a split", ErrInvalid, n.Pane)
			}
			if seen[n.Pane] {
				return fmt.Errorf("%w: pane %q appears twice", ErrInvalid, n.Pane)
			}
			seen[n.Pane] = true
			return nil
		case n.Split != Horizontal && n.Split != Vertical:
			return fmt.Errorf("%w: split %q (want horizontal or vertical)", ErrInvalid, n.Split)
		case n.First == nil || n.Second == nil:
			return fmt.Errorf("%w: a split needs two children", ErrInvalid)
		case math.IsNaN(n.Ratio) || n.Ratio < MinRatio || n.Ratio > MaxRatio:
			return fmt.Errorf("%w: ratio %v outside %v..%v", ErrInvalid, n.Ratio, MinRatio, MaxRatio)
		}
		if err := check(n.First); err != nil {
			return err
		}
		return check(n.Second)
	}
	return check(n)
}

// Normalize fills in defaults a hand-written tree may omit (a zero ratio
// becomes DefaultRatio) and clamps ratios into range.
func (n *Node) Normalize() {
	if n == nil || n.IsLeaf() {
		return
	}
	if n.Ratio == 0 {
		n.Ratio = DefaultRatio
	}
	n.Ratio = clampRatio(n.Ratio)
	n.First.Normalize()
	n.Second.Normalize()
}

func clampRatio(r float64) float64 {
	if math.IsNaN(r) {
		return DefaultRatio
	}
	return math.Max(MinRatio, math.Min(MaxRatio, r))
}

// Split puts newPane next to target in direction d, sharing target's area:
// target keeps 1-ratio of it and the new pane gets ratio. It returns the new
// root (unchanged when the root is not target's leaf).
func Split(root *Node, target string, d Direction, ratio float64, newPane string) (*Node, error) {
	if newPane == "" || root.Contains(newPane) {
		return root, fmt.Errorf("%w: new pane %q is empty or already present", ErrInvalid, newPane)
	}
	if ratio == 0 {
		ratio = DefaultRatio
	}
	ratio = clampRatio(ratio)
	p, ok := root.path(target)
	if !ok {
		return root, ErrNotFound
	}
	leaf := p[len(p)-1]
	old, added := Leaf(leaf.Pane), Leaf(newPane)
	*leaf = Node{Split: d.orientation()}
	if d.forward() {
		leaf.First, leaf.Second, leaf.Ratio = old, added, 1-ratio
	} else {
		leaf.First, leaf.Second, leaf.Ratio = added, old, ratio
	}
	return root, nil
}

// Remove takes pane out of the tree; its sibling takes the parent's place.
// The result is nil when pane was the only one.
func Remove(root *Node, pane string) (*Node, error) {
	p, ok := root.path(pane)
	if !ok {
		return root, ErrNotFound
	}
	if len(p) == 1 {
		return nil, nil
	}
	parent := p[len(p)-2]
	sibling := parent.First
	if parent.First == p[len(p)-1] {
		sibling = parent.Second
	}
	*parent = *sibling
	return root, nil
}

// Swap exchanges two panes' places.
func Swap(root *Node, a, b string) error {
	pa, okA := root.path(a)
	pb, okB := root.path(b)
	if !okA || !okB {
		return ErrNotFound
	}
	la, lb := pa[len(pa)-1], pb[len(pb)-1]
	la.Pane, lb.Pane = lb.Pane, la.Pane
	return nil
}

// Rename replaces pane from by to (for a pane moved in from elsewhere).
func Rename(root *Node, from, to string) error {
	p, ok := root.path(from)
	if !ok {
		return ErrNotFound
	}
	if root.Contains(to) {
		return fmt.Errorf("%w: pane %q already present", ErrInvalid, to)
	}
	p[len(p)-1].Pane = to
	return nil
}

// Resize moves the border on pane's d side by cells, growing the pane, in a
// tab of the given area. Negative cells shrink it. The nearest enclosing
// split with a border on that side is adjusted.
func Resize(root *Node, area Rect, pane string, d Direction, cells int) error {
	p, ok := root.path(pane)
	if !ok {
		return ErrNotFound
	}
	rects := splitRects(root, area)
	for i := len(p) - 2; i >= 0; i-- {
		split, child := p[i], p[i+1]
		if split.Split != d.orientation() {
			continue
		}
		// The border is on d's side of child when child is the first one and
		// d points forward, or the second one and d points back.
		inFirst := split.First == child
		if inFirst != d.forward() {
			continue
		}
		r := rects[split]
		total := r.W - 1
		if split.Split == Vertical {
			total = r.H - 1
		}
		if total <= 1 {
			return ErrNoBorder
		}
		delta := float64(cells) / float64(total)
		if !inFirst {
			delta = -delta // growing the second child moves the border back
		}
		split.Ratio = clampRatio(split.Ratio + delta)
		return nil
	}
	return ErrNoBorder
}
