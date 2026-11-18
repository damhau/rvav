// Pieces the administration pages share (S9, ADR-0020).
import { useQuery } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import type { UserRef } from "@/lib/api/types";
import { useDebounced } from "@/lib/hooks";
import { useCurrentProject } from "@/lib/project";
import { adm, q } from "@/lib/query";

type S = components["schemas"];
export type Offer = S["Offer"];

export const th = "border-b border-line px-3 py-2 font-medium";
export const td = "border-b border-line px-3 py-2 align-top";

/** The server's problems, by field pointer. */
export function fieldErrors(e: unknown): Record<string, string> {
  if (!isApiError(e)) return {};
  return Object.fromEntries(e.fieldErrors.map((f) => [f.field, f.message]));
}

/** What an edit dialog sends back: the stored object without what only the server sets. */
export function writable<T extends object, K extends keyof T>(v: T, ...keys: K[]): Omit<T, K> {
  const out: Partial<T> = { ...v };
  for (const k of keys) delete out[k];
  return out as Omit<T, K>;
}

/**
 * Problems with rows of keys and values as typed, by row: a key missing on
 * a row that isn't blank (blank rows are left out), or a key given twice.
 */
export function keyProblems<T extends { key: string }>(
  rows: T[],
  blank: (row: T) => boolean,
  missing: string,
): Record<number, string> {
  const out: Record<number, string> = {};
  const seen = new Set<string>();
  rows.forEach((r, i) => {
    const k = r.key.trim();
    if (!k) {
      if (!blank(r)) out[i] = missing;
    } else if (seen.has(k)) out[i] = `${k} is given twice.`;
    seen.add(k);
  });
  return out;
}

/** A priority typed in a box: empty is the default, 100. */
export const priorityOf = (v: string) => (v.trim() === "" ? 100 : Number(v));

/** The problem at a pointer, or the first one under it (`/roles/2` for `/roles`). */
export function errorAt(errors: Record<string, string>, pointer: string): string | undefined {
  return errors[pointer] ?? Object.entries(errors).find(([k]) => k.startsWith(`${pointer}/`))?.[1];
}

/** A failed change, said in a toast (field problems go next to their field). */
export function failed(e: unknown, fallback: string) {
  toast.error(isApiError(e) ? e.problem.title : fallback, {
    description: isApiError(e) ? (e.fieldErrors[0]?.message ?? e.problem.detail) : undefined,
  });
}

/** Finds people by name or email. */
export function UserPicker({ label, id, onPick }: { label: string; id: string; onPick: (u: UserRef) => void }) {
  const [text, setText] = useState("");
  const search = useDebounced(text, 250);
  const users = useQuery({ ...adm.users(search), enabled: search.length >= 2 });
  // People with the same name and email can exist (an identity provider that
  // changed their subject): mark the one signed in.
  const { me } = useCurrentProject();
  return (
    <div className="relative flex flex-col gap-1">
      <Field label={label} htmlFor={id}>
        <Input
          id={id}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="Name or email"
          autoComplete="off"
        />
      </Field>
      {search.length >= 2 && users.data && (
        <ul
          aria-label={`${label}: people found`}
          className="flex flex-col rounded-md border border-line bg-surface shadow-pop"
        >
          {users.data.items.length === 0 && <li className="px-3 py-1.5 text-secondary text-muted">Nobody found.</li>}
          {users.data.items.map((u) => (
            <li key={u.id}>
              <button
                type="button"
                className="w-full px-3 py-1.5 text-left text-body hover:bg-sunken focus-visible:bg-sunken focus-visible:outline-none"
                onClick={() => {
                  onPick(u);
                  setText("");
                }}
              >
                {u.name} <span className="text-secondary text-faint">{u.email}</span>
                {u.id === me.user.id && <span className="text-secondary text-faint"> (you)</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** Where something is offered: projects and environments, "all" for none chosen. */
export function OfferEditor({
  value,
  onChange,
  error,
}: {
  value: Offer[];
  onChange: (v: Offer[]) => void;
  error?: string;
}) {
  const projects = useQuery(adm.projectNames());
  const envs = useQuery(q.environments());
  const projectOptions = [
    { value: "all", label: "All projects" },
    ...(projects.data?.items ?? []).map((p) => ({ value: p.id, label: p.name })),
  ];
  const envOptions = [
    { value: "all", label: "All environments" },
    ...(envs.data?.items ?? []).map((e) => ({ value: e.key, label: e.name })),
  ];
  const set = (i: number, o: Offer) => onChange(value.map((x, j) => (j === i ? o : x)));
  return (
    <fieldset className="flex flex-col gap-1.5">
      <legend className="mb-1 text-secondary font-medium">Offered to</legend>
      {error && <p className="text-secondary text-bad">{error}</p>}
      {value.length === 0 && <p className="text-secondary text-faint">Nobody yet.</p>}
      {value.map((o, i) => (
        <div key={i} className="flex items-center gap-2">
          <Select
            aria-label={`Offer ${i + 1}: project`}
            value={o.project_id ?? "all"}
            onChange={(v) => set(i, { ...o, project_id: v === "all" ? null : v })}
            options={projectOptions}
            className="w-48"
          />
          <Select
            aria-label={`Offer ${i + 1}: environment`}
            value={o.environment ?? "all"}
            onChange={(v) => set(i, { ...o, environment: v === "all" ? null : v })}
            options={envOptions}
            className="w-44"
          />
          <Button
            type="button"
            size="icon"
            variant="quiet"
            aria-label={`Remove offer ${i + 1}`}
            onClick={() => onChange(value.filter((_, j) => j !== i))}
          >
            <X className="size-3.5" />
          </Button>
        </div>
      ))}
      <div>
        <Button
          type="button"
          size="sm"
          variant="quiet"
          onClick={() => onChange([...value, { project_id: null, environment: null }])}
        >
          Add an offer
        </Button>
      </div>
    </fieldset>
  );
}

/** Says where something is offered, in words. */
export function offersText(offers: Offer[], projectName: (id: string) => string): string {
  if (offers.length === 0) return "Nobody";
  return offers
    .map(
      (o) =>
        `${o.project_id ? projectName(o.project_id) : "All projects"}${o.environment ? ` in ${o.environment}` : ""}`,
    )
    .join("; ");
}
