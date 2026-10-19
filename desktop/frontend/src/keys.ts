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

// Mouse modes a program can ask for, and how reports are encoded.
export const ModeFocusEvents = 1 << 3;
export const ModeMouseX10 = 1 << 4;
export const ModeMouseNormal = 1 << 5;
export const ModeMouseButton = 1 << 6;
export const ModeMouseAny = 1 << 7;
export const ModeMouseSGR = 1 << 9;

export type MouseKind = "press" | "release" | "move" | "wheel-up" | "wheel-down";

export interface MouseReport {
  kind: MouseKind;
  button: number; // 0 left, 1 middle, 2 right
  x: number; // cell, from 0
  y: number;
  shift: boolean;
  alt: boolean;
  ctrl: boolean;
  buttons: number; // pressed buttons, for moves
}

// mouseWanted says the program asked for mouse reports.
export function mouseWanted(modes: number): boolean {
  return (modes & (ModeMouseX10 | ModeMouseNormal | ModeMouseButton | ModeMouseAny)) !== 0;
}

// encodeMouse returns the report for a mouse event, as xterm sends it, or
// null when the program did not ask for this kind of event.
export function encodeMouse(m: MouseReport, modes: number): string | null {
  if (!mouseWanted(modes)) return null;
  const x10 = (modes & ModeMouseX10) !== 0 && (modes & (ModeMouseNormal | ModeMouseButton | ModeMouseAny)) === 0;
  let code: number;
  switch (m.kind) {
    case "wheel-up":
      if (x10) return null;
      code = 64;
      break;
    case "wheel-down":
      if (x10) return null;
      code = 65;
      break;
    case "move": {
      const dragging = m.buttons !== 0;
      if (!((modes & ModeMouseAny) !== 0 || ((modes & ModeMouseButton) !== 0 && dragging))) return null;
      code = 32 + (dragging ? (m.buttons & 1 ? 0 : m.buttons & 4 ? 1 : 2) : 3);
      break;
    }
    case "release":
      if (x10) return null;
      code = (modes & ModeMouseSGR) !== 0 ? m.button : 3;
      break;
    default:
      code = m.button;
  }
  if (!x10) code += (m.shift ? 4 : 0) + (m.alt ? 8 : 0) + (m.ctrl ? 16 : 0);
  const x = m.x + 1,
    y = m.y + 1;
  if ((modes & ModeMouseSGR) !== 0) return `\x1b[<${code};${x};${y}${m.kind === "release" ? "m" : "M"}`;
  if (x > 223 || y > 223) return null; // the legacy encoding cannot say it
  return `\x1b[M${String.fromCharCode(32 + code, 32 + x, 32 + y)}`;
}

// focusReport is what a program that asked for focus events gets.
export function focusReport(focused: boolean, modes: number): string | null {
  return (modes & ModeFocusEvents) !== 0 ? (focused ? "\x1b[I" : "\x1b[O") : null;
}

// quotePaths quotes dropped file paths for a shell.
export function quotePaths(paths: string[], windows: boolean): string {
  return paths
    .map((p) => (windows ? `"${p.replace(/"/g, '""')}"` : /^[A-Za-z0-9_./:@%+=,-]+$/.test(p) ? p : `'${p.replace(/'/g, `'\\''`)}'`))
    .join(" ");
}
