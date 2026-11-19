// Soft policies' warnings as the server names and checks them (ADR-0045).
import type { components } from "@/lib/api/schema";

type PolicyWarning = components["schemas"]["PolicyWarning"];
type FieldError = components["schemas"]["FieldError"];

/** A warning, named the same while its policy and message are; the server's digest is another. */
export function warning(policy: string, message: string): PolicyWarning {
  let h = 0x811c9dc5;
  for (const c of `${policy}\0${message}`) h = Math.imul(h ^ c.codePointAt(0)!, 0x01000193) >>> 0;
  return { id: `w${h.toString(16).padStart(8, "0")}`, policy, message };
}

/**
 * The refusal's errors when the draft doesn't accept every warning, none when it may go on: the
 * warnings it lists decide, else `accept_warnings` accepts any.
 */
export function unaccepted(
  warnings: PolicyWarning[],
  draft: { accept_warnings?: boolean; accepted_warnings?: string[] },
): FieldError[] {
  const listed = draft.accepted_warnings;
  const ok = listed ? warnings.every((w) => listed.includes(w.id)) : Boolean(draft.accept_warnings);
  if (warnings.length === 0 || ok) return [];
  return warnings.map((w) => ({ field: "/accept_warnings", code: "warning", message: w.message, warning: w }));
}
