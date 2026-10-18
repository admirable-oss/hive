// Key encoding: what a terminal sends for a key press, as xterm does.
// Printable text is not encoded here: it arrives through the input
// element's input and composition events, which handle IMEs, dead keys and
// keyboard layouts.

export const ModeAppCursorKeys = 1;
export const ModeBracketedPaste = 1 << 2;

export interface KeyPress {
  key: string;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  metaKey: boolean;
}

const ESC = "\x1b";

// modifier is xterm's modifier parameter: 1 + shift + 2·alt + 4·ctrl.
function modifier(e: KeyPress): number {
  return 1 + (e.shiftKey ? 1 : 0) + (e.altKey ? 2 : 0) + (e.ctrlKey ? 4 : 0);
}

const cursorKeys: Record<string, string> = { ArrowUp: "A", ArrowDown: "B", ArrowRight: "C", ArrowLeft: "D", Home: "H", End: "F" };
const tildeKeys: Record<string, number> = {
  Insert: 2, Delete: 3, PageUp: 5, PageDown: 6,
  F5: 15, F6: 17, F7: 18, F8: 19, F9: 20, F10: 21, F11: 23, F12: 24,
};
const ss3Keys: Record<string, string> = { F1: "P", F2: "Q", F3: "R", F4: "S" };

// ctrlChar is the control character Ctrl+key sends, if any.
function ctrlChar(key: string): string | null {
  if (key.length !== 1) return null;
  const c = key.toLowerCase();
  if (c >= "a" && c <= "z") return String.fromCharCode(c.charCodeAt(0) - 96);
  switch (c) {
    case " ":
    case "@":
    case "2":
      return "\x00";
    case "[":
    case "3":
      return ESC;
    case "\\":
    case "4":
      return "\x1c";
    case "]":
    case "5":
      return "\x1d";
    case "^":
    case "6":
      return "\x1e";
    case "_":
    case "-":
    case "/":
    case "7":
      return "\x1f";
    case "8":
    case "?":
      return "\x7f";
  }
  return null;
}

// encodeKey returns the bytes for a key press, or null when the key is
// left to the input element (text) or to the app (shortcuts). optionIsMeta
// makes macOS's Option key act as Alt, as terminals let users choose.
export function encodeKey(e: KeyPress, modes: number, mac: boolean, optionIsMeta = false): string | null {
  if (e.metaKey) return null; // ⌘ shortcuts belong to the app
  const alt = e.altKey && (!mac || optionIsMeta);
  const prefix = alt ? ESC : "";
  const mod = modifier({ ...e, altKey: alt });

  if (e.key in cursorKeys) {
    const final = cursorKeys[e.key];
    if (mod > 1) return `${ESC}[1;${mod}${final}`;
    const app = (modes & ModeAppCursorKeys) !== 0;
    return app ? `${ESC}O${final}` : `${ESC}[${final}`;
  }
  if (e.key in tildeKeys) {
    const n = tildeKeys[e.key];
    return mod > 1 ? `${ESC}[${n};${mod}~` : `${ESC}[${n}~`;
  }
  if (e.key in ss3Keys) {
    const final = ss3Keys[e.key];
    return mod > 1 ? `${ESC}[1;${mod}${final}` : `${ESC}O${final}`;
  }
  switch (e.key) {
    case "Enter":
      return prefix + "\r";
    case "Tab":
      return e.shiftKey ? `${ESC}[Z` : prefix + "\t";
    case "Backspace":
      return prefix + (e.ctrlKey ? "\x08" : "\x7f");
    case "Escape":
      return prefix + ESC;
  }
  if (e.ctrlKey && !e.shiftKey) {
    const c = ctrlChar(e.key);
    if (c !== null) return prefix + c;
  }
  if (e.ctrlKey && e.shiftKey && !mac) return null; // Ctrl+Shift+C/V: the app's copy and paste
  if (alt && e.key.length === 1) return ESC + e.key;
  return null;
}
