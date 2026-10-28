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
import { adm, q } from "@/lib/query";

type S = components["schemas"];
export type Offer = S["Offer"];
export type ApprovalLevel = S["ApprovalLevel"];

export const th = "border-b border-line px-3 py-2 font-medium";
export const td = "border-b border-line px-3 py-2 align-top";

/** The server's problems, by field pointer. */
export function fieldErrors(e: unknown): Record<string, string> {
  if (!isApiError(e)) return {};
  return Object.fromEntries(e.fieldErrors.map((f) => [f.field, f.message]));
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
          {users.data.items.length === 0 && <li className="px-3 py-1.5 text-xs text-muted">Nobody found.</li>}
          {users.data.items.map((u) => (
            <li key={u.id}>
              <button
                type="button"
                className="w-full px-3 py-1.5 text-left text-[13px] hover:bg-sunken focus-visible:bg-sunken focus-visible:outline-none"
                onClick={() => {
                  onPick(u);
                  setText("");
                }}
              >
                {u.name} <span className="text-xs text-faint">{u.email}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** Where something is offered: projects and environments, "all" for none chosen. */
export function OfferEditor({ value, onChange }: { value: Offer[]; onChange: (v: Offer[]) => void }) {
  const projects = useQuery(adm.projects("", false));
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
      <legend className="mb-1 text-xs font-medium">Offered to</legend>
      {value.length === 0 && <p className="text-xs text-faint">Nobody yet.</p>}
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
            variant="ghost"
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
          variant="ghost"
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

/** The levels of an approval: who approves, how many, and what happens on time-out. */
export function LevelsEditor({
  value,
  onChange,
  errors,
}: {
  value: ApprovalLevel[];
  onChange: (v: ApprovalLevel[]) => void;
  errors: Record<string, string>;
}) {
  const set = (i: number, l: ApprovalLevel) => onChange(value.map((x, j) => (j === i ? l : x)));
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-1 text-xs font-medium">Approval levels, in order</legend>
      {errors["/levels"] && <p className="text-xs text-bad">{errors["/levels"]}</p>}
      {value.map((l, i) => (
        <div
          key={i}
          className="grid grid-cols-1 gap-2 rounded-md border border-line p-2 sm:grid-cols-[1fr_1fr_110px_100px_130px_auto]"
        >
          <Field label="Level" htmlFor={`level-${i}-name`}>
            <Input id={`level-${i}-name`} value={l.name} onChange={(e) => set(i, { ...l, name: e.target.value })} />
          </Field>
          <Field label="OIDC group" htmlFor={`level-${i}-group`} hint={errors[`/levels/${i}`]}>
            <Input
              id={`level-${i}-group`}
              value={l.group ?? ""}
              onChange={(e) => set(i, { ...l, group: e.target.value || undefined })}
            />
          </Field>
          <Field label="Who" htmlFor={`level-${i}-mode`}>
            <Select
              id={`level-${i}-mode`}
              value={l.mode ?? "any"}
              onChange={(v) => set(i, { ...l, mode: v as ApprovalLevel["mode"] })}
              options={[
                { value: "any", label: "Any one" },
                { value: "all", label: "All" },
              ]}
            />
          </Field>
          <Field label="Time-out" htmlFor={`level-${i}-timeout`} hint={errors[`/levels/${i}/timeout`]}>
            <Input
              id={`level-${i}-timeout`}
              value={l.timeout ?? ""}
              placeholder="48h"
              onChange={(e) => set(i, { ...l, timeout: e.target.value || undefined })}
            />
          </Field>
          <Field label="Then" htmlFor={`level-${i}-then`}>
            <Select
              id={`level-${i}-then`}
              value={l.on_timeout ?? "none"}
              onChange={(v) =>
                set(i, { ...l, on_timeout: v === "none" ? undefined : (v as ApprovalLevel["on_timeout"]) })
              }
              options={[
                { value: "none", label: "Wait" },
                { value: "escalate", label: "Escalate" },
                { value: "approve", label: "Approve" },
                { value: "reject", label: "Reject" },
              ]}
            />
          </Field>
          <div className="self-end">
            <Button
              type="button"
              size="icon"
              variant="ghost"
              aria-label={`Remove level ${i + 1}`}
              onClick={() => onChange(value.filter((_, j) => j !== i))}
            >
              <X className="size-3.5" />
            </Button>
          </div>
        </div>
      ))}
      <div>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => onChange([...value, { name: `Level ${value.length + 1}`, mode: "any" }])}
        >
          Add a level
        </Button>
      </div>
    </fieldset>
  );
}
