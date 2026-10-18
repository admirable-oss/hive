import { useSyncExternalStore } from "react";
import { useStore } from "../store";
import { palette, schemeFor, type Scheme } from "../theme";

const dark = typeof window !== "undefined" && window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)") : undefined;

function systemDark(): boolean {
  return dark?.matches ?? true;
}

// useScheme is the colour scheme in effect: the configured theme's, or the
// system's for "auto".
export function useScheme(): Scheme {
  const theme = useStore((s) => s.settings.theme);
  const sys = useSyncExternalStore(
    (l) => {
      dark?.addEventListener("change", l);
      return () => dark?.removeEventListener("change", l);
    },
    systemDark,
  );
  return schemeFor(theme, sys);
}

const palettes = { dark: palette("dark"), light: palette("light") };

export function usePalette(): string[] {
  return palettes[useScheme()];
}
