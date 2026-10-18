import { encodeKey, ModeAppCursorKeys, type KeyPress } from "./keys";

const k = (key: string, mods: Partial<KeyPress> = {}): KeyPress => ({
  key,
  ctrlKey: false,
  altKey: false,
  shiftKey: false,
  metaKey: false,
  ...mods,
});

describe("encodeKey", () => {
  it("encodes editing keys", () => {
    expect(encodeKey(k("Enter"), 0, false)).toBe("\r");
    expect(encodeKey(k("Backspace"), 0, false)).toBe("\x7f");
    expect(encodeKey(k("Tab"), 0, false)).toBe("\t");
    expect(encodeKey(k("Tab", { shiftKey: true }), 0, false)).toBe("\x1b[Z");
    expect(encodeKey(k("Escape"), 0, false)).toBe("\x1b");
    expect(encodeKey(k("Delete"), 0, false)).toBe("\x1b[3~");
    expect(encodeKey(k("PageUp"), 0, false)).toBe("\x1b[5~");
  });

  it("follows the application cursor mode", () => {
    expect(encodeKey(k("ArrowUp"), 0, false)).toBe("\x1b[A");
    expect(encodeKey(k("ArrowUp"), ModeAppCursorKeys, false)).toBe("\x1bOA");
    expect(encodeKey(k("ArrowLeft", { ctrlKey: true }), ModeAppCursorKeys, false)).toBe("\x1b[1;5D");
    expect(encodeKey(k("End", { shiftKey: true }), 0, false)).toBe("\x1b[1;2F");
  });

  it("encodes function keys", () => {
    expect(encodeKey(k("F1"), 0, false)).toBe("\x1bOP");
    expect(encodeKey(k("F5"), 0, false)).toBe("\x1b[15~");
    expect(encodeKey(k("F12", { altKey: true }), 0, false)).toBe("\x1b[24;3~");
  });

  it("sends control characters", () => {
    expect(encodeKey(k("c", { ctrlKey: true }), 0, false)).toBe("\x03");
    expect(encodeKey(k("d", { ctrlKey: true }), 0, true)).toBe("\x04");
    expect(encodeKey(k(" ", { ctrlKey: true }), 0, false)).toBe("\x00");
    expect(encodeKey(k("[", { ctrlKey: true }), 0, false)).toBe("\x1b");
  });

  it("prefixes Alt with ESC, but leaves macOS Option to type characters", () => {
    expect(encodeKey(k("b", { altKey: true }), 0, false)).toBe("\x1bb");
    expect(encodeKey(k("∫", { altKey: true }), 0, true)).toBeNull();
    expect(encodeKey(k("b", { altKey: true }), 0, true, true)).toBe("\x1bb");
  });

  it("leaves text and shortcuts alone", () => {
    expect(encodeKey(k("a"), 0, false)).toBeNull();
    expect(encodeKey(k("A", { shiftKey: true }), 0, false)).toBeNull();
    expect(encodeKey(k("c", { metaKey: true }), 0, true)).toBeNull();
    expect(encodeKey(k("C", { ctrlKey: true, shiftKey: true }), 0, false)).toBeNull();
  });
});
