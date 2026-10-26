import { useSyncExternalStore } from "react";

export type Theme = "system" | "light" | "dark";

const KEY = "rvav.theme";
const listeners = new Set<() => void>();
let current: Theme = getStoredTheme();

export function getStoredTheme(): Theme {
  try {
    const v = globalThis.localStorage?.getItem(KEY);
    return v === "light" || v === "dark" ? v : "system";
  } catch {
    return "system";
  }
}

/** `system` leaves `data-theme` unset so prefers-color-scheme decides. */
export function applyTheme(theme: Theme) {
  const root = globalThis.document?.documentElement;
  if (!root) return;
  if (theme === "system") delete root.dataset.theme;
  else root.dataset.theme = theme;
}

export function setTheme(theme: Theme) {
  current = theme;
  applyTheme(theme);
  try {
    globalThis.localStorage?.setItem(KEY, theme);
  } catch {
    // Storage can be blocked; the choice then lasts for this page only.
  }
  listeners.forEach((fn) => fn());
}

const subscribe = (fn: () => void) => {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
};

export function useTheme() {
  const theme = useSyncExternalStore(subscribe, () => current);
  return { theme, setTheme };
}
