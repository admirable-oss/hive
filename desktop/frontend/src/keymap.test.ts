import { commandFor, controlBytes, isPrefix, keyNames, lookup } from "./keymap";

const key = (k: string, mods: Partial<{ ctrl: boolean; alt: boolean; shift: boolean; meta: boolean; code: string }> = {}) => ({
  key: k,
  code: mods.code ?? "",
  ctrlKey: !!mods.ctrl,
  altKey: !!mods.alt,
  shiftKey: !!mods.shift,
  metaKey: !!mods.meta,
});

describe("keyNames", () => {
  it("names keys as hive's [keys] does", () => {
    expect(keyNames(key("b", { ctrl: true, code: "KeyB" }))).toEqual(["ctrl+b"]);
    expect(keyNames(key("%", { shift: true, code: "Digit5" }))).toEqual(["%"]);
    expect(keyNames(key("H", { shift: true, code: "KeyH" }))).toEqual(["H"]);
    expect(keyNames(key("ArrowLeft", { alt: true }))).toEqual(["alt+left"]);
    expect(keyNames(key("Tab", { shift: true }))).toEqual(["shift+tab"]);
    expect(keyNames(key(" "))).toEqual(["space"]);
    expect(keyNames(key("Escape"))).toEqual(["esc"]);
    expect(keyNames(key("A", { ctrl: true, shift: true, code: "KeyA" }))).toEqual(["ctrl+shift+a"]);
  });
  it("names Option combinations by the physical key, not the character macOS composes", () => {
    expect(keyNames(key("ƒ", { alt: true, code: "KeyF" }))).toEqual(["alt+f"]);
    expect(keyNames(key("¡", { alt: true, code: "Digit1" }))).toEqual(["alt+1"]);
  });
  it("leaves ⌘ and lone modifiers alone", () => {
    expect(keyNames(key("k", { meta: true, code: "KeyK" }))).toEqual([]);
    expect(keyNames(key("Shift", { shift: true }))).toEqual([]);
  });
});

describe("bindings", () => {
  const bindings = [
    { mode: "prefix", action: "split-right", keys: ["%", "|"] },
    { mode: "prefix", action: "send-prefix", keys: ["ctrl+a"] },
    { mode: "navigate", action: "focus-left", keys: ["h"] },
  ];
  it("finds the action a key runs in a mode", () => {
    expect(lookup(bindings, "prefix", ["|"])).toBe("split-right");
    expect(lookup(bindings, "prefix", ["h"])).toBeUndefined();
    expect(isPrefix(["ctrl+a"], ["ctrl+a"])).toBe(true);
    expect(isPrefix(["ctrl+a"], ["ctrl+b"])).toBe(false);
  });
  it("maps hive's actions to the app's commands", () => {
    expect(commandFor("split-right")).toBe("split-right");
    expect(commandFor("edit-scrollback")).toBe("history");
    expect(commandFor("tab-3")).toBe("tab-3");
    expect(commandFor("detach")).toBeUndefined();
  });
  it("sends a prefix key pressed twice to the pane", () => {
    expect(controlBytes("ctrl+b")).toBe("\x02");
    expect(controlBytes("ctrl+a")).toBe("\x01");
    expect(controlBytes("alt+left")).toBeNull();
  });
});
