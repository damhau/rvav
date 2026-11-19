// A query's key stays in TanStack Query's cache, as a mutation's variables
// do: a secret input never goes in one (as RemoteAccess keeps the private
// key out of both). The key holds a stand-in instead, the same for the
// same value and another for another, so a changed secret still fetches.
import { useCallback, useRef } from "react";
import type { UiField } from "@/lib/api/types";

export const isSecretField = (ui: UiField | undefined) => ui?.widget === "secret" || Boolean(ui?.secret);

/** A secret value's stand-in, for a query's key; an empty value stays as it is. */
export function useStandIn(): (value: unknown) => unknown {
  const seen = useRef(new Map<unknown, number>());
  return useCallback((v: unknown) => {
    if (v === undefined || v === null || v === "") return v;
    let n = seen.current.get(v);
    if (n === undefined) {
      n = seen.current.size + 1;
      seen.current.set(v, n);
    }
    return `secret #${n}`;
  }, []);
}

/** Replaces the secret inputs of `fields` with stand-ins, for a query's key. */
export function useSecretStandIns(
  fields: Record<string, UiField>,
): <T extends Record<string, unknown>>(inputs: T) => T {
  const standIn = useStandIn();
  return useCallback(
    <T extends Record<string, unknown>>(inputs: T): T => {
      let out: Record<string, unknown> = inputs;
      for (const [name, v] of Object.entries(inputs)) {
        if (!isSecretField(fields[name])) continue;
        const kept = standIn(v);
        if (kept === v) continue;
        if (out === inputs) out = { ...inputs };
        out[name] = kept;
      }
      return out as T;
    },
    [fields, standIn],
  );
}
