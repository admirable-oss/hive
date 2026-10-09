// Package vt is Hive's terminal-emulation layer. It turns an agent's PTY
// output into a screen of cells that can be snapshotted, diffed into frames,
// sent over the wire and painted on any terminal.
//
// Emulation is delegated to charmbracelet/x/vt (see docs/adr/0001). This
// package owns everything around it: a compact screen model independent of
// the emulator's types, terminal-mode tracking, scrollback with a byte
// budget, the frame codec and the ANSI painter. Nothing outside this package
// imports x/vt, so the emulator can be replaced without touching callers.
package vt
