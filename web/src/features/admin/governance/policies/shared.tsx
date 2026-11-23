import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Button, IconButton } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { Field, FieldNote, Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import type { components } from "@/lib/api/schema";
import { can, canAnywhere } from "@/lib/can";
import { formatList } from "@/lib/format";
import { i18n } from "@/lib/i18n";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { errorAt, failed } from "../common";
import { roleName } from "@/lib/i18n/names";

type ApprovalLevel = components["schemas"]["ApprovalLevel"];
type RoleRef = components["schemas"]["RoleRef"];
type UserRef = components["schemas"]["UserRef"];

export const list = (s: string) =>
  s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);

/**
 * The projects the page names: every one for those who read every policy,
 * else those whose own policies the caller writes. A policy is written for
 * the latter only, unless the caller writes every policy (ADR-0045).
 */
export function useProjects() {
  const { me } = useCurrentProject();
  const everyone = can(me, "policies.read");
  const global = can(me, "policies.manage");
  const projects = useQuery({ ...adm.projectNames(), enabled: everyone });
  const own = me.projects.filter((p) => p.permissions.includes("project.policies.manage")).map((p) => p.project);
  const items = everyone ? (projects.data?.items ?? []) : own;
  const writable = global ? items : own;
  return {
    items,
    name: (id: string) => items.find((p) => p.id === id)?.name ?? id,
    /** The projects a policy may be written for, every project first when the caller writes those. */
    options: (all: string) => [
      ...(global ? [{ value: "all", label: all }] : []),
      ...writable.map((p) => ({ value: p.id, label: p.name })),
    ],
    /** The project a new policy is for: every project, or the caller's first. */
    initial: global ? null : (writable[0]?.id ?? null),
  };
}

/**
 * Refreshes a kind of policy and what policies decide elsewhere: deployments'
 * actions and update previews, and request previews. It resolves once the
 * policies are read again.
 */
export function useRefreshPolicies(key: string) {
  const qc = useQueryClient();
  return () => {
    for (const queryKey of [["deployments"], ["requests", "preview"]]) void qc.invalidateQueries({ queryKey });
    return qc.invalidateQueries({ queryKey: ["admin", key] });
  };
}

interface Owned {
  project_id?: string | null;
  set_by_admin?: boolean;
}

/** Under a project's policy: that an administrator set it, so the project's admins leave it alone (ADR-0045). */
export function SetByAdmin({ policy }: { policy: Owned }) {
  const { t } = useTranslation("admin-governance");
  if (!policy.project_id || !policy.set_by_admin) return null;
  return <div className="text-caption text-faint">{t("policies.setByAdmin")}</div>;
}

/** A soft policy warns where a hard one refuses or asks for approval (ADR-0045). */
export const enforcementOptions = () => [
  { value: "hard", label: i18n.t("admin-governance:policies.enforcement.hard") },
  { value: "soft", label: i18n.t("admin-governance:policies.enforcement.soft") },
];

/** What the caller writes: every policy with policies.manage, else their projects' own (ADR-0045). */
export function usePolicyRights() {
  const { me } = useCurrentProject();
  const global = can(me, "policies.manage");
  const writes = (projectId?: string | null) =>
    global || (Boolean(projectId) && can(me, "project.policies.manage", projectId ?? undefined));
  return {
    /** Writes the policies of every project. */
    global,
    /** Writes some policy. */
    any: global || canAnywhere(me, "project.policies.manage"),
    /** Writes the policy of this project, or of every project for null. */
    writes,
    /** Changes this policy: a project admin leaves alone those an administrator set. */
    changes: (p: Owned) => global || (!p.set_by_admin && writes(p.project_id)),
  };
}

export type PolicyRights = ReturnType<typeof usePolicyRights>;

/** Why a project admin's menu doesn't move policies: it renumbers every project's too. */
export const movesEveryone = (rights: PolicyRights) =>
  rights.global ? undefined : i18n.t("admin-governance:policies.movesEveryone");

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
  const { t } = useTranslation("admin-governance");
  const refresh = useRefreshPolicies(key);
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
    onError: (e) => failed(e, t("policies.notMoved")),
  });
  const update = useMutation({
    mutationFn: ({ policy }: { policy: T; done: string }) => put(policy),
    onSuccess: (_, { done }) => toast.success(done),
    onSettled: refresh,
    onError: (e) => failed(e, t("policies.notSaved")),
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
  const { t } = useTranslation("admin-governance");
  const busy = ops.move.isPending || ops.update.isPending;
  const waits = busy ? t("policies.menu.waits") : moveWhy;
  const up = waits ?? (index === 0 ? t("policies.menu.first") : undefined);
  const down = waits ?? (index === items.length - 1 ? t("policies.menu.last") : undefined);
  return (
    <>
      <MenuItem onSelect={onChange}>{t("policies.menu.change")}</MenuItem>
      <MenuSeparator />
      <MenuItem disabled={Boolean(up)} onSelect={() => ops.move.mutate({ items, from: index, to: index - 1 })}>
        <WithReason label={t("policies.menu.moveUp")} why={up} />
      </MenuItem>
      <MenuItem disabled={Boolean(down)} onSelect={() => ops.move.mutate({ items, from: index, to: index + 1 })}>
        <WithReason label={t("policies.menu.moveDown")} why={down} />
      </MenuItem>
      <MenuItem onSelect={onPriority}>{t("policies.menu.setPriority")}</MenuItem>
      <MenuSeparator />
      <MenuItem
        disabled={busy}
        onSelect={() =>
          ops.update.mutate({
            policy: { ...policy, enabled: !policy.enabled },
            done: t(policy.enabled ? "policies.turnedOff" : "policies.turnedOn", { name: policy.name }),
          })
        }
      >
        {policy.enabled ? t("policies.menu.turnOff") : t("policies.menu.turnOn")}
      </MenuItem>
      <MenuItem danger onSelect={onRemove}>
        {t("policies.menu.remove")}
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
  const { t } = useTranslation(["admin-governance", "common"]);
  const [value, setValue] = useState(String(policy.priority));
  const input = useRef<HTMLInputElement>(null);
  const priority = Number(value);
  const valid = value.trim() !== "" && Number.isInteger(priority);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title={t("policies.priorityDialog.title", { name: policy.name })}
        description={t("policies.priorityDialog.lead")}
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
              {
                policy: { ...policy, priority },
                done: t("policies.priorityDialog.done", { name: policy.name, priority }),
              },
              { onSuccess: onClose },
            );
          }}
        >
          <Field label={t("policies.priority")} htmlFor="policy-priority" className="w-40">
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
              {t("common:actions.cancel")}
            </Button>
            <Button type="submit" variant="primary" disabled={!valid || ops.update.isPending}>
              {t("policies.menu.setPriority")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/**
 * Who a level asks, in a sentence: its group, users, project roles and the requester's manager. A user missing
 * from `users` (the policy list's) shows by ID.
 */
export function askedText(l: ApprovalLevel, roles: RoleRef[], users: UserRef[]): string {
  const name = (k: string) => {
    const r = roles.find((x) => x.key === k);
    return r ? roleName(r) : k;
  };
  const person = (id: string) => users.find((u) => u.id === id)?.name ?? id;
  const parts = [
    l.group,
    l.approvers?.length ? l.approvers.map(person).join(", ") : undefined,
    l.roles?.length ? formatList(l.roles.map(name), "disjunction") : undefined,
    l.manager ? i18n.t("admin-governance:policies.levels.managerInText") : undefined,
  ].filter(Boolean);
  return parts.length ? parts.join(", ") : l.name;
}

/**
 * Roles to tick, by name, in a group named `legend`: `roles` and any key in
 * `picked` they don't have. An error marks the first one, so a refused save
 * moves the focus there.
 */
export function RoleChecks({
  id,
  legend,
  roles,
  picked,
  onChange,
  hint,
  error,
}: {
  id: string;
  legend: string;
  roles: RoleRef[];
  picked: string[];
  onChange: (picked: string[]) => void;
  hint: string;
  error?: string;
}) {
  const shown = [...roles, ...picked.filter((k) => !roles.some((r) => r.key === k)).map((k) => ({ key: k, name: k }))];
  const note = `${id}-note`;
  return (
    <fieldset className="flex flex-col gap-1" aria-describedby={note}>
      <legend className="mb-1 text-secondary font-medium text-fg">{legend}</legend>
      <div className="flex flex-wrap gap-x-4 gap-y-1.5">
        {shown.map((r, i) => (
          <div key={r.key} className="flex items-center gap-1.5">
            <Checkbox
              id={`${id}-${r.key}`}
              checked={picked.includes(r.key)}
              aria-invalid={error && i === 0 ? true : undefined}
              aria-describedby={error ? note : undefined}
              onChange={(on) => onChange(on ? [...picked, r.key] : picked.filter((k) => k !== r.key))}
            />
            <label htmlFor={`${id}-${r.key}`} className="text-body">
              {roleName(r)}
            </label>
          </div>
        ))}
      </div>
      <FieldNote id={note} error={error} hint={hint} />
    </fieldset>
  );
}

// A Go duration, the same in every language.
const timeoutExample = "48h";

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
  const { t } = useTranslation("admin-governance");
  return (
    <div className="flex flex-col gap-3 pr-9">
      <RoleChecks
        id={`level-${i}-role`}
        legend={t("policies.levels.roles")}
        roles={roles}
        picked={l.roles ?? []}
        onChange={(picked) => set({ ...l, roles: picked.length ? picked : undefined })}
        hint={t("policies.levels.rolesHint")}
        error={errorAt(errors, `/levels/${i}/roles`)}
      />
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
            {t("policies.levels.manager")}
          </label>
          <p id={`level-${i}-manager-note`} className="text-secondary text-muted">
            {t("policies.levels.managerHint")}
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
  const { t } = useTranslation("admin-governance");
  const set = (i: number, l: ApprovalLevel) => onChange(value.map((x, j) => (j === i ? l : x)));
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-1 text-secondary font-medium">{t("policies.levels.legend")}</legend>
      {errors["/levels"] && <p className="text-secondary text-bad">{errors["/levels"]}</p>}
      {value.map((l, i) => {
        const at = (k: string) => errors[`/levels/${i}${k}`];
        return (
          <div key={i} className="flex flex-col gap-3 rounded-md border border-line p-3">
            <div className="flex items-start gap-2">
              <div className="grid flex-1 grid-cols-2 gap-3">
                <Field label={t("policies.levels.level")} htmlFor={`level-${i}-name`} error={at("/name")}>
                  <Input
                    id={`level-${i}-name`}
                    value={l.name}
                    onChange={(e) => set(i, { ...l, name: e.target.value })}
                  />
                </Field>
                <Field
                  label={t("policies.levels.group")}
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
                label={t("policies.levels.remove", { n: i + 1 })}
                className="mt-6"
                onClick={() => onChange(value.filter((_, j) => j !== i))}
              >
                <X />
              </IconButton>
            </div>
            <LevelAsks i={i} l={l} roles={roles} errors={errors} set={(next) => set(i, next)} />
            <div className="grid grid-cols-3 gap-3 pr-9">
              <Field label={t("policies.levels.who")} htmlFor={`level-${i}-mode`}>
                <Select
                  id={`level-${i}-mode`}
                  value={l.mode ?? "any"}
                  onChange={(v) => set(i, { ...l, mode: v as ApprovalLevel["mode"] })}
                  options={[
                    { value: "any", label: t("policies.levels.anyOne") },
                    { value: "all", label: t("policies.levels.all") },
                  ]}
                />
              </Field>
              <Field label={t("policies.levels.timeout")} htmlFor={`level-${i}-timeout`} error={at("/timeout")}>
                <Input
                  id={`level-${i}-timeout`}
                  value={l.timeout ?? ""}
                  placeholder={timeoutExample}
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
              <Field label={t("policies.levels.then")} htmlFor={`level-${i}-then`} error={at("/on_timeout")}>
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
                    { value: "none", label: t("policies.levels.wait") },
                    { value: "escalate", label: t("policies.levels.escalate") },
                    { value: "approve", label: t("policies.levels.approve") },
                    { value: "reject", label: t("policies.levels.reject") },
                  ]}
                />
              </Field>
            </div>
            {l.on_timeout === "escalate" && (
              <Field
                label={t("policies.levels.escalateTo")}
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
          onClick={() =>
            onChange([...value, { name: t("policies.levels.named", { n: value.length + 1 }), mode: "any" }])
          }
        >
          {t("policies.levels.add")}
        </Button>
      </div>
    </fieldset>
  );
}
