// Package layout is the tiling model of a tab: a binary space partition
// whose leaves are panes. It is pure (no I/O, no clocks), so every operation
// is a function of its inputs and is tested exhaustively.
//
// A split divides an area between two children, side by side (Horizontal)
// or stacked (Vertical); Ratio is the first child's share. Geometry places
// a one-cell separator between children, where a client draws the border.
package layout
