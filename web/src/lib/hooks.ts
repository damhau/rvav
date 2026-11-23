import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";

/** The value, once it has stopped changing for `ms`. */
export function useDebounced<T>(value: T, ms: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return debounced;
}

/**
 * A search box over a search kept elsewhere, in the address or a list's filters. The box shows each key at
 * once; the search follows, trimmed, once typing pauses for `ms`. When the search changes without the box
 * (Back, a link, Clear the filters), the box shows it. A new `cleared` (Clear the filters, counted) drops
 * what was typed and not yet searched.
 */
export function useSearchBox(
  search: string | undefined,
  onSearch: (next: string | undefined) => void,
  { ms = 250, cleared }: { ms?: number; cleared?: number } = {},
): [string, (text: string) => void] {
  const [text, setText] = useState(search ?? "");
  const [sent, setSent] = useState(search);
  const [seen, setSeen] = useState(search);
  const [seenCleared, setSeenCleared] = useState(cleared);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  if (search !== seen) {
    setSeen(search);
    if (search !== sent) {
      setSent(search);
      setText(search ?? "");
    }
  }
  if (cleared !== seenCleared) {
    setSeenCleared(cleared);
    setSent(search);
    setText(search ?? "");
  }
  // A new `cleared` drops the search waiting to be sent, with the effect.
  useEffect(() => () => clearTimeout(timer.current), [cleared]);
  const type = (next: string) => {
    setText(next);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      const trimmed = next.trim() || undefined;
      setSent(trimmed);
      onSearch(trimmed);
    }, ms);
  };
  return [text, type];
}

/** Re-render every `ms` so relative times and running durations stay current. */
export function useNow(ms = 1000) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(t);
  }, [ms]);
  return now;
}

/** Whether a CSS media query matches, kept current as the window changes. */
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      const list = window.matchMedia(query);
      list.addEventListener("change", onChange);
      return () => list.removeEventListener("change", onChange);
    },
    [query],
  );
  return useSyncExternalStore(subscribe, () => window.matchMedia(query).matches);
}
