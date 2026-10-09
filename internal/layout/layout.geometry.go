package layout

import "math"

// Geometry returns each visible pane's area within area. Children of a split
// are separated by one cell (the border). Panes too small to show get a zero
// size rather than a negative one. With zoom set to a pane in the tree, that
// pane fills the whole area and no other pane is visible.
func Geometry(root *Node, area Rect, zoom string) map[string]Rect {
	out := map[string]Rect{}
	if root == nil {
		return out
	}
	if zoom != "" && root.Contains(zoom) {
		out[zoom] = area
		return out
	}
	for n, r := range splitRects(root, area) {
		if n.IsLeaf() {
			out[n.Pane] = r
		}
	}
	return out
}

// splitRects returns the area of every node.
func splitRects(root *Node, area Rect) map[*Node]Rect {
	out := map[*Node]Rect{}
	var place func(*Node, Rect)
	place = func(n *Node, r Rect) {
		if n == nil {
			return
		}
		out[n] = r
		if n.IsLeaf() {
			return
		}
		a, b := divide(r, n.Split, n.Ratio)
		place(n.First, a)
		place(n.Second, b)
	}
	place(root, area)
	return out
}

// divide splits r into two areas with a one-cell separator between them.
func divide(r Rect, o Orientation, ratio float64) (Rect, Rect) {
	// A child too small to show still sits inside the parent's area.
	if o == Horizontal {
		w1, w2 := shares(r.W, ratio)
		return Rect{r.X, r.Y, w1, r.H}, Rect{min(r.X+w1+1, r.X+r.W), r.Y, w2, r.H}
	}
	h1, h2 := shares(r.H, ratio)
	return Rect{r.X, r.Y, r.W, h1}, Rect{r.X, min(r.Y+h1+1, r.Y+r.H), r.W, h2}
}

// shares divides size-1 cells (one goes to the separator) by ratio. Each
// side keeps at least one cell when there is room for both.
func shares(size int, ratio float64) (int, int) {
	avail := size - 1
	if avail < 2 {
		return max(avail, 0), 0
	}
	first := int(math.Round(float64(avail) * ratio))
	first = min(max(first, 1), avail-1)
	return first, avail - first
}

// Neighbor returns the pane next to pane in direction d: the one whose area
// lies beyond pane's border on that side and overlaps it the most.
func Neighbor(root *Node, area Rect, pane string, d Direction) (string, bool) {
	rects := Geometry(root, area, "")
	from, ok := rects[pane]
	if !ok {
		return "", false
	}
	best, bestOverlap, bestDist := "", -1, math.MaxInt
	for id, r := range rects {
		if id == pane {
			continue
		}
		var dist, overlap int
		switch d {
		case Left:
			dist, overlap = from.X-(r.X+r.W), span(from.Y, from.H, r.Y, r.H)
		case Right:
			dist, overlap = r.X-(from.X+from.W), span(from.Y, from.H, r.Y, r.H)
		case Up:
			dist, overlap = from.Y-(r.Y+r.H), span(from.X, from.W, r.X, r.W)
		case Down:
			dist, overlap = r.Y-(from.Y+from.H), span(from.X, from.W, r.X, r.W)
		}
		if dist < 0 || overlap <= 0 {
			continue
		}
		if dist < bestDist || (dist == bestDist && overlap > bestOverlap) {
			best, bestOverlap, bestDist = id, overlap, dist
		}
	}
	return best, best != ""
}

// span returns how much [a, a+al) and [b, b+bl) overlap.
func span(a, al, b, bl int) int {
	return min(a+al, b+bl) - max(a, b)
}
