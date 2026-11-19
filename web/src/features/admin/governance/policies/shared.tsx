import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { Button, IconButton } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { Field, FieldNote, Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import type { components } from "@/lib/api/schema";
import { can, canAnywhere } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { errorAt, failed } from "../common";

type ApprovalLevel = components["schemas"]["ApprovalLevel"];
type RoleRef = components["schemas"]["RoleRef"];

export const list = (s: string) =>
  s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);

/**
 * The projects the page names and picks: every one for those who read
 * every policy, else those whose own policies the caller writes (ADR-0045).
 */
export function useProjects() {
  const { me } = useCurrentProject();
  const everyone = can(me, "policies.read");
  const global = can(me, "policies.manage");
  const projects = useQuery({ ...adm.projectNames(), enabled: everyone });
  const items = everyone
    ? (projects.data?.items ?? [])
    : me.projects.filter((p) => p.permissions.includes("project.policies.manage")).map((p) => p.project);
  return {
    items,
    name: (id: string) => items.find((p) => p.id === id)?.name ?? id,
    /** The projects a policy may be written for, every project first when the caller writes those. */
    options: (all: string) => [
      ...(global ? [{ value: "all", label: all }] : []),
      ...items.map((p) => ({ value: p.id, label: p.name })),
    ],
    /** The project a new policy is for: every project, or the caller's first. */
    initial: global ? null : (items[0]?.id ?? null),
  };
}

/** A soft policy warns where a hard one refuses or asks for approval (ADR-0045). */
export const enforcementOptions = [
  { value: "hard", label: "Hard: it refuses, or waits for approval" },
  { value: "soft", label: "Soft: it warns, and the requester may go on" },
];

/** What the caller writes: every policy with policies.manage, else their projects' own (ADR-0045). */
export function usePolicyRights() {
  const { me } = useCurrentProject();
  const global = can(me, "policies.manage");
  return {
    /** Writes the policies of every project. */
    global,
    /** Writes some policy. */
    any: global || canAnywhere(me, "project.policies.manage"),
    /** Writes the policy of this project, or of every project for null. */
    writes: (projectId?: string | null) =>
      global || (Boolean(projectId) && can(me, "project.policies.manage", projectId ?? undefined)),
  };
}

export type PolicyRights = ReturnType<typeof usePolicyRights>;

/** Why a project admin's menu doesn't move policies: it renumbers every project's too. */
export const movesEveryone = (rights: PolicyRights) =>
  rights.global ? undefined : "It renumbers the policies of every project: set its priority instead";

interface Ordered {
  id: string;
  name: string;
  priority: number;
  enabled: boolean;
}

/**
 * The changes an ordered policy's row menu makes, each a PUT of the whole
 * policy: moving numbers the list in its new order 10, 20, 30…, so ties
 * can't keep a policy in place, and saves only the policies whose priority
 * changes.
 */
export function useOrderedPolicies<T extends Ordered>(key: string, put: (p: T) => Promise<unknown>) {
  const qc = useQueryClient();
  const refresh = () => void qc.invalidateQueries({ queryKey: ["admin", key] });
  const move = useMutation({
    mutationFn: async ({ items, from, to }: { items: T[]; from: number; to: number }) => {
      const order = [...items];
      const [moved] = order.splice(from, 1);
      order.splice(to, 0, moved!);
      for (const [i, p] of order.entries()) {
        if (p.priority !== (i + 1) * 10) await put({ ...p, priority: (i + 1) * 10 });
      }
    },
    onSettled: refresh,
    onError: (e) => failed(e, "Not moved"),
  });
  const update = useMutation({
    mutationFn: ({ policy }: { policy: T; done: string }) => put(policy),
    onSuccess: (_, { done }) => toast.success(done),
    onSettled: refresh,
    onError: (e) => failed(e, "Not saved"),
  });
  return { move, update };
}

/** A menu item's label, with why it is off under it. */
function WithReason({ label, why }: { label: string; why?: string }) {
  if (!why) return label;
  return (
    <span className="flex min-w-0 flex-col">
      <span>{label}</span> <span className="text-caption leading-snug text-faint">{why}</span>
    </span>
  );
}

/** A row menu's items for a policy whose place in the list matters. */
export function OrderedMenu<T extends Ordered>({
  policy,
  items,
  index,
  ops,
  onChange,
  onPriority,
  onRemove,
  moveWhy,
}: {
  policy: T;
  items: T[];
  index: number;
  ops: ReturnType<typeof useOrderedPolicies<T>>;
  onChange: () => void;
  onPriority: () => void;
  onRemove: () => void;
  /** Why moving is off, whatever the policy's place. */
  moveWhy?: string;
}) {
  const busy = ops.move.isPending || ops.update.isPending;
  const waits = busy ? "Waits for the change running" : moveWhy;
  const up = waits ?? (index === 0 ? "It comes first already" : undefined);
  const down = waits ?? (index === items.length - 1 ? "It comes last already" : undefined);
  return (
    <>
      <MenuItem onSelect={onChange}>Change</MenuItem>
      <MenuSeparator />
      <MenuItem disabled={Boolean(up)} onSelect={() => ops.move.mutate({ items, from: index, to: index - 1 })}>
        <WithReason label="Move up" why={up} />
      </MenuItem>
      <MenuItem disabled={Boolean(down)} onSelect={() => ops.move.mutate({ items, from: index, to: index + 1 })}>
        <WithReason label="Move down" why={down} />
      </MenuItem>
      <MenuItem onSelect={onPriority}>Set priority</MenuItem>
      <MenuSeparator />
      <MenuItem
        disabled={busy}
        onSelect={() =>
          ops.update.mutate({
            policy: { ...policy, enabled: !policy.enabled },
            done: `${policy.name} turned ${policy.enabled ? "off" : "on"}`,
          })
        }
      >
        {policy.enabled ? "Turn off" : "Turn on"}
      </MenuItem>
      <MenuItem danger onSelect={onRemove}>
        Remove
      </MenuItem>
    </>
  );
}

/** Asks for a policy's priority: a question, not the policy's whole form. */
export function PriorityDialog<T extends Ordered>({
  policy,
  ops,
  onClose,
}: {
  policy: T;
  ops: ReturnType<typeof useOrderedPolicies<T>>;
  onClose: () => void;
}) {
  const [value, setValue] = useState(String(policy.priority));
  const input = useRef<HTMLInputElement>(null);
  const priority = Number(value);
  const valid = value.trim() !== "" && Number.isInteger(priority);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title={`Priority of ${policy.name}`}
        description="Lower comes first."
        onOpenAutoFocus={(e) => {
          e.preventDefault();
          input.current?.select();
        }}
      >
        <form
          className="flex flex-col gap-4 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            ops.update.mutate(
              { policy: { ...policy, priority }, done: `${policy.name} at priority ${priority}` },
              { onSuccess: onClose },
            );
          }}
        >
          <Field label="Priority" htmlFor="policy-priority" className="w-40">
            <Input
              id="policy-priority"
              type="number"
              step={1}
              ref={input}
              value={value}
              onChange={(e) => setValue(e.target.value)}
            />
          </Field>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="secondary" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={!valid || ops.update.isPending}>
              Set priority
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** Who a level asks, in a sentence: its group, users, project roles and the requester's manager. */
export function askedText(l: ApprovalLevel, roles: RoleRef[]): string {
  const name = (k: string) => roles.find((r) => r.key === k)?.name ?? k;
  const parts = [
    l.group,
    l.approvers?.length ? l.approvers.join(", ") : undefined,
    l.roles?.length ? l.roles.map(name).join(" or ") : undefined,
    l.manager ? "the requester's manager" : undefined,
  ].filter(Boolean);
  return parts.length ? parts.join(", ") : l.name;
}

/** The project roles a level asks, and whether it asks the requester's manager. */
function LevelAsks({
  i,
  l,
  roles,
  errors,
  set,
}: {
  i: number;
  l: ApprovalLevel;
  roles: RoleRef[];
  errors: Record<string, string>;
  set: (l: ApprovalLevel) => void;
}) {
  const picked = l.roles ?? [];
  const shown = [...roles, ...picked.filter((k) => !roles.some((r) => r.key === k)).map((k) => ({ key: k, name: k }))];
  const error = errorAt(errors, `/levels/${i}/roles`);
  const toggle = (key: string, on: boolean) => {
    const next = on ? [...picked, key] : picked.filter((k) => k !== key);
    set({ ...l, roles: next.length ? next : undefined });
  };
  return (
    <div className="flex flex-col gap-3 pr-9">
      <fieldset className="flex flex-col gap-1" aria-describedby={`level-${i}-roles-note`}>
        <legend className="mb-1 text-secondary font-medium text-fg">Project roles</legend>
        <div className="flex flex-wrap gap-x-4 gap-y-1.5">
          {shown.map((r) => {
            const id = `level-${i}-role-${r.key}`;
            return (
              <div key={r.key} className="flex items-center gap-1.5">
                <Checkbox id={id} checked={picked.includes(r.key)} onChange={(on) => toggle(r.key, on)} />
                <label htmlFor={id} className="text-body">
                  {r.name}
                </label>
              </div>
            );
          })}
        </div>
        <FieldNote
          id={`level-${i}-roles-note`}
          error={error}
          hint="Whoever holds one in the request's project when the level opens, themselves or through a group."
        />
      </fieldset>
      <div className="flex items-start gap-2">
        <Checkbox
          id={`level-${i}-manager`}
          className="mt-0.5"
          aria-describedby={`level-${i}-manager-note`}
          checked={l.manager ?? false}
          onChange={(manager) => set({ ...l, manager: manager || undefined })}
        />
        <div className="flex flex-col">
          <label htmlFor={`level-${i}-manager`} className="text-body">
            The requester's manager
          </label>
          <p id={`level-${i}-manager-note`} className="text-secondary text-muted">
            From the identity provider's manager claim. Without one, the level asks only its other approvers.
          </p>
        </div>
      </div>
    </div>
  );
}

/** The levels of an approval: who approves, how many, and what happens on time-out. */
export function LevelsEditor({
  value,
  onChange,
  errors,
  roles,
}: {
  value: ApprovalLevel[];
  onChange: (v: ApprovalLevel[]) => void;
  errors: Record<string, string>;
  /** The project roles a level may name. */
  roles: RoleRef[];
}) {
  const set = (i: number, l: ApprovalLevel) => onChange(value.map((x, j) => (j === i ? l : x)));
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-1 text-secondary font-medium">Approval levels, in order</legend>
      {errors["/levels"] && <p className="text-secondary text-bad">{errors["/levels"]}</p>}
      {value.map((l, i) => {
        const at = (k: string) => errors[`/levels/${i}${k}`];
        return (
          <div key={i} className="flex flex-col gap-3 rounded-md border border-line p-3">
            <div className="flex items-start gap-2">
              <div className="grid flex-1 grid-cols-2 gap-3">
                <Field label="Level" htmlFor={`level-${i}-name`} error={at("/name")}>
                  <Input
                    id={`level-${i}-name`}
                    value={l.name}
                    onChange={(e) => set(i, { ...l, name: e.target.value })}
                  />
                </Field>
                <Field
                  label="OIDC group"
                  htmlFor={`level-${i}-group`}
                  error={at("") ?? errorAt(errors, `/levels/${i}/approvers`)}
                >
                  <Input
                    id={`level-${i}-group`}
                    value={l.group ?? ""}
                    onChange={(e) => set(i, { ...l, group: e.target.value || undefined })}
                  />
                </Field>
              </div>
              <IconButton
                type="button"
                label={`Remove level ${i + 1}`}
                className="mt-6"
                onClick={() => onChange(value.filter((_, j) => j !== i))}
              >
                <X />
              </IconButton>
            </div>
            <LevelAsks i={i} l={l} roles={roles} errors={errors} set={(next) => set(i, next)} />
            <div className="grid grid-cols-3 gap-3 pr-9">
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
              <Field label="Time-out" htmlFor={`level-${i}-timeout`} error={at("/timeout")}>
                <Input
                  id={`level-${i}-timeout`}
                  value={l.timeout ?? ""}
                  placeholder="48h"
                  onChange={(e) =>
                    set(
                      i,
                      e.target.value
                        ? { ...l, timeout: e.target.value }
                        : { ...l, timeout: undefined, on_timeout: undefined, escalate_to: undefined },
                    )
                  }
                />
              </Field>
              <Field label="Then" htmlFor={`level-${i}-then`} error={at("/on_timeout")}>
                <Select
                  id={`level-${i}-then`}
                  value={l.on_timeout ?? "none"}
                  disabled={!l.timeout}
                  onChange={(v) =>
                    set(i, {
                      ...l,
                      on_timeout: v === "none" ? undefined : (v as ApprovalLevel["on_timeout"]),
                      escalate_to: v === "escalate" ? (l.escalate_to ?? {}) : undefined,
                    })
                  }
                  options={[
                    { value: "none", label: "Wait" },
                    { value: "escalate", label: "Escalate" },
                    { value: "approve", label: "Approve" },
                    { value: "reject", label: "Reject" },
                  ]}
                />
              </Field>
            </div>
            {l.on_timeout === "escalate" && (
              <Field
                label="Escalate to OIDC group"
                htmlFor={`level-${i}-escalate`}
                error={errorAt(errors, `/levels/${i}/escalate_to`)}
                className="pr-9"
              >
                <Input
                  id={`level-${i}-escalate`}
                  value={l.escalate_to?.group ?? ""}
                  onChange={(e) =>
                    set(i, { ...l, escalate_to: { ...l.escalate_to, group: e.target.value || undefined } })
                  }
                />
              </Field>
            )}
          </div>
        );
      })}
      <div>
        <Button
          type="button"
          size="sm"
          variant="quiet"
          onClick={() => onChange([...value, { name: `Level ${value.length + 1}`, mode: "any" }])}
        >
          Add a level
        </Button>
      </div>
    </fieldset>
  );
}
