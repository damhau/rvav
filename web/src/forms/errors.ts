import type { FieldError } from "@/lib/api/types";

export interface MappedErrors {
  /** Messages keyed by input name. */
  fields: Record<string, string>;
  /** Errors not tied to an input (quota, environment, …). */
  form: string[];
}

/** Turn problem `errors` (JSON pointers like `/inputs/role`) into per-field messages. */
export function mapFieldErrors(errors: FieldError[] | undefined): MappedErrors {
  const out: MappedErrors = { fields: {}, form: [] };
  for (const e of errors ?? []) {
    const m = /^\/inputs\/([^/]+)/.exec(e.field);
    const name = m?.[1];
    if (name) out.fields[name] ??= e.message;
    else if (e.field === "/environment") out.fields.$environment ??= e.message;
    else out.form.push(e.message);
  }
  return out;
}
