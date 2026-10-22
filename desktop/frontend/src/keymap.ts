// The [keys] section of the Hive configuration in the app: the prefix key,
// then one key that runs an action, as in `hive ui`. The bindings come from
// `hive config keys` (Settings.keys), with the built-in ones and the user's
// changes applied, keys already in hive's normal form ("ctrl+b", "%", "A",
// "alt+left", "space").
import type { KeyBinding } from "./api";

type KeyLike = Pick<KeyboardEvent, "key" | "code" | "ctrlKey" | "altKey" | "metaKey" | "shiftKey">;

const named: Record<string, string> = {
  ArrowUp: "up",
  ArrowDown: "down",
  ArrowLeft: "left",
  ArrowRight: "right",
  Escape: "esc",
  Enter: "enter",
  Tab: "tab",
  Backspace: "backspace",
  " ": "space",
  Delete: "delete",
  Insert: "insert",
  Home: "home",
  End: "end",
  PageUp: "pgup",
  PageDown: "pgdown",
};

const modifierKeys = new Set(["Shift", "Control", "Alt", "Meta", "CapsLock", "Dead"]);

// keyNames is what a key press may match a binding by, most specific first,
// as hive matches them: the character it types ("%", "A", "?"), then its
// keystroke ("ctrl+b", "alt+left", "shift+tab"). ⌘ belongs to the app's
// own shortcuts: a press with it matches nothing.
export function keyNames(e: KeyLike): string[] {
  if (e.metaKey || modifierKeys.has(e.key)) return [];
  let base = named[e.key] ?? (/^F\d{1,2}$/.test(e.key) ? e.key.toLowerCase() : undefined);
  if (!base) {
    let ch = e.key;
    if (e.ctrlKey || e.altKey) {
      // With Ctrl or Option the character is a control or composed one
      // ("ƒ" for Option+F on macOS): the physical key names it.
      const letter = /^Key([A-Z])$/.exec(e.code);
      const digit = /^Digit(\d)$/.exec(e.code);
      if (letter) ch = letter[1].toLowerCase();
      else if (digit) ch = digit[1];
      else ch = ch.toLowerCase();
    }
    if ([...ch].length !== 1) return [];
    if (!e.ctrlKey && !e.altKey) return [ch];
    base = ch;
  }
  const mods = [e.ctrlKey && "ctrl", e.altKey && "alt", e.shiftKey && "shift"].filter(Boolean) as string[];
  return [[...mods, base].join("+")];
}

// lookup returns the action a key press runs in mode, if any.
export function lookup(bindings: KeyBinding[], mode: string, names: string[]): string | undefined {
  for (const n of names) {
    const b = bindings.find((b) => b.mode === mode && b.keys.includes(n));
    if (b) return b.action;
  }
  return undefined;
}

// isPrefix says whether a key press is one of the prefix keys.
export function isPrefix(prefixes: string[], names: string[]): boolean {
  return names.some((n) => prefixes.includes(n));
}

// controlBytes is what a prefix key sends to the pane when it is pressed
// twice (send-prefix): ctrl+<letter> is its control character.
export function controlBytes(key: string): string | null {
  const m = /^ctrl\+([a-z])$/.exec(key);
  if (m) return String.fromCharCode(m[1].charCodeAt(0) - 96);
  if (key === "ctrl+space") return "\x00";
  return [...key].length === 1 ? key : null;
}

// Hive actions the app runs, by the command that does it. Actions missing
// here (popups, modes, copy mode, detach) belong to the terminal UI.
const commands: Record<string, string> = {
  "split-right": "split-right",
  "split-down": "split-down",
  "close-pane": "close-pane",
  zoom: "zoom",
  "new-tab": "new-tab",
  "close-tab": "close-tab",
  "next-tab": "next-tab",
  "prev-tab": "prev-tab",
  "next-env": "next-env",
  "prev-env": "prev-env",
  "focus-left": "focus-left",
  "focus-right": "focus-right",
  "focus-up": "focus-up",
  "focus-down": "focus-down",
  "focus-next": "focus-next",
  "focus-prev": "focus-prev",
  "swap-next": "swap-next",
  "swap-prev": "swap-prev",
  "resize-left": "resize-left",
  "resize-right": "resize-right",
  "resize-up": "resize-up",
  "resize-down": "resize-down",
  goto: "goto",
  palette: "palette",
  "toggle-sidebar": "toggle-sidebar",
  "edit-scrollback": "history",
  "next-agent": "next-attention",
  "open-notification-target": "next-attention",
  paste: "paste",
};

// commandFor is the app's command for a hive action; undefined when the
// app has none.
export function commandFor(action: string): string | undefined {
  if (commands[action]) return commands[action];
  if (/^tab-[1-9]$/.test(action) || /^focus-agent-[1-9]$/.test(action)) return action;
  return undefined;
}
