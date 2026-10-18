// Terminal colours: the 16 ANSI colours per theme, and xterm's 256-colour
// palette built from them.

const dark16 = [
  "#1b1f27", "#f07178", "#a6da95", "#f2c66d", "#7aa2f7", "#c79bf2", "#7dcfff", "#c8ccd4",
  "#5c6370", "#ff8b92", "#c3e88d", "#ffd580", "#9ab8ff", "#dcb6ff", "#a3e4ff", "#f0f3f6",
];
const light16 = [
  "#24292f", "#cf222e", "#116329", "#9a6700", "#0550ae", "#8250df", "#1b7c83", "#6e7781",
  "#57606a", "#a40e26", "#1a7f37", "#7d4e00", "#0969da", "#6639ba", "#3192aa", "#8c959f",
];

function cube(): string[] {
  const steps = [0, 95, 135, 175, 215, 255];
  const out: string[] = [];
  for (let r = 0; r < 6; r++)
    for (let g = 0; g < 6; g++)
      for (let b = 0; b < 6; b++) out.push(hex(steps[r], steps[g], steps[b]));
  for (let i = 0; i < 24; i++) {
    const v = 8 + i * 10;
    out.push(hex(v, v, v));
  }
  return out;
}

function hex(r: number, g: number, b: number): string {
  return "#" + [r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("");
}

const extended = cube();

export type Scheme = "dark" | "light";

export function palette(scheme: Scheme): string[] {
  return [...(scheme === "dark" ? dark16 : light16), ...extended];
}

// color turns a wire colour ("", "0"–"255" or "#rrggbb") into CSS; "" is
// the theme's foreground or background (undefined).
export function color(c: string | undefined, pal: string[]): string | undefined {
  if (!c) return undefined;
  if (c[0] === "#") return c;
  const i = Number(c);
  return Number.isInteger(i) && i >= 0 && i < pal.length ? pal[i] : undefined;
}

// Light themes Hive knows; anything else configured is dark, and "auto" or
// "terminal" follow the system.
const lightThemes = new Set(["catppuccin-latte", "light"]);

export function schemeFor(theme: string, systemDark: boolean): Scheme {
  if (theme === "auto" || theme === "terminal" || theme === "") return systemDark ? "dark" : "light";
  return lightThemes.has(theme) ? "light" : "dark";
}
