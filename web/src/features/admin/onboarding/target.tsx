// Where adopted VMs go (X1.5): each into a deployment of its own, all into
// one new deployment, or into an existing deployment as VMs of one of its
// components. Adopting at once and onboarding plans share these fields.
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Plus, X } from "lucide-react";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import type { components } from "@/lib/api/schema";
import { i18n } from "@/lib/i18n";
import { useEnvName } from "@/lib/environments";
import { adm, q } from "@/lib/query";
import { keyProblems } from "../governance/common";

type S = components["schemas"];

export type Into = "each" | "one" | "existing";

export interface Target {
  project: string;
  into: Into;
  environment: string;
  owner: string;
  item: string;
  lease: string;
  name: string;
  deployment: string;
  component: string;
  inputs: { key: string; value: string }[];
}

/**
 * After the server refuses, the focus goes to the first field it names. `groups` gives, for an
 * error on a whole group of fields (such as `/filter`), the id of the group's first input; pass
 * a constant, as a new object each render would move the focus each render.
 */
export function useFocusRefused(errors: Record<string, string>, groups: Record<string, string> = noGroups) {
  useEffect(() => {
    if (Object.keys(errors).length === 0) return;
    const sheet = document.querySelector<HTMLElement>('[role="dialog"]');
    const named = [
      ...(sheet?.querySelectorAll<HTMLElement>('[aria-invalid="true"]') ?? []),
      ...Object.entries(groups).flatMap(([path, id]) => (errors[path] ? [document.getElementById(id)] : [])),
    ].filter((e): e is HTMLElement => e !== null);
    named.sort((a, b) => (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1));
    named[0]?.focus();
  }, [errors, groups]);
}
const noGroups: Record<string, string> = {};

export const emptyTarget: Target = {
  project: "",
  into: "each",
  environment: "",
  owner: "",
  item: "",
  lease: "",
  name: "",
  deployment: "",
  component: "",
  inputs: [],
};

/** A value typed as text: numbers, true, false and JSON lists and objects are read as such. */
function literal(v: string): unknown {
  const t = v.trim();
  if (!/^(-?\d+(\.\d+)?|true|false|\[[\s\S]*\]|\{[\s\S]*\})$/.test(t)) return v;
  try {
    return JSON.parse(t);
  } catch {
    return v;
  }
}
const shown = (v: unknown) => (typeof v === "string" ? v : JSON.stringify(v));

export function inputsOf(t: Target): Record<string, unknown> | undefined {
  const rows = t.inputs.filter((r) => r.key.trim() !== "");
  return rows.length ? Object.fromEntries(rows.map((r) => [r.key.trim(), literal(r.value)])) : undefined;
}

/** Problems with the inputs' keys, by row, before anything is sent. */
export const inputProblems = (target: Target) =>
  keyProblems(target.inputs, (r) => r.value === "", i18n.t("admin-onboarding:target.nameInput"));

const given = (v: string) => (v.trim() === "" ? undefined : v.trim());

/** A plan's target as the API takes it: what isn't given is left out. */
export function planTarget(t: Target): S["OnboardingTarget"] {
  const component = given(t.component);
  if (t.into === "existing") return { project_id: t.project, deployment_id: t.deployment, component };
  return {
    project_id: t.project,
    environment: given(t.environment),
    owner_id: given(t.owner),
    catalog_item_id: given(t.item),
    lease_days: given(t.lease) ? Number(t.lease) : undefined,
    deployment_name: t.into === "one" ? given(t.name) : undefined,
    component,
    inputs: inputsOf(t),
  };
}

export function targetOf(t: S["OnboardingTarget"]): Target {
  return {
    project: t.project_id,
    into: t.deployment_id ? "existing" : t.deployment_name ? "one" : "each",
    environment: t.environment ?? "",
    owner: t.owner_id ?? "",
    item: t.catalog_item_id ?? "",
    lease: t.lease_days ? String(t.lease_days) : "",
    name: t.deployment_name ?? "",
    deployment: t.deployment_id ?? "",
    component: t.component ?? "",
    inputs: Object.entries(t.inputs ?? {}).map(([key, v]) => ({ key, value: shown(v) })),
  };
}

const intos: Into[] = ["each", "one", "existing"];
const nameExample = "erp-prd";

/**
 * The fields of a target. `at` is where the server's errors about it are
 * (`/target` for a plan's), `id` prefixes the fields' IDs, and `current`
 * is the deployment a plan joins already, offered even while it isn't ready.
 */
export function TargetFields({
  t,
  onChange,
  errors,
  at,
  id,
  local,
  current,
}: {
  t: Target;
  onChange: (t: Target) => void;
  errors: Record<string, string>;
  at: string;
  id: string;
  /** Problems with the inputs found before saving, by row. */
  local: Record<number, string>;
  current?: S["DeploymentRef"];
}) {
  const { t: tr } = useTranslation("admin-onboarding");
  const set = (patch: Partial<Target>) => onChange({ ...t, ...patch });
  const projects = useQuery(adm.projectNames());
  const project = useQuery({ ...q.project(t.project), enabled: t.project !== "" });
  const envName = useEnvName();
  const members = useQuery({ ...adm.members(t.project), enabled: t.project !== "" && t.into !== "existing" });
  // Owners run the actions on their deployments: members who may request
  // there. Without the roles to read, every member is offered; the server
  // decides.
  const roles = useQuery(adm.roles()).data?.items;
  const requests = (m: S["ProjectMember"]) =>
    !roles || m.roles.some((r) => roles.find((x) => x.key === r.key)?.permissions.includes("request.submit"));
  const items = useQuery({
    ...q.catalog({ projectId: t.project, type: "vm_blueprint" }),
    enabled: t.project !== "",
  });
  // Only a VM item's deployment takes VMs: those of the project's VM items, every page of them.
  const slugs = items.data?.items.map((i) => i.slug) ?? [];
  const deployments = useInfiniteQuery({
    ...q.deployments({ projectId: t.project, status: ["ready"], item: slugs, limit: 100 }),
    enabled: t.project !== "" && t.into === "existing" && slugs.length > 0,
  });
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = deployments;
  useEffect(() => {
    if (hasNextPage && !isFetchingNextPage) void fetchNextPage();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);
  const ready = slugs.length > 0 ? (deployments.data?.pages.flatMap((p) => p.items) ?? []) : [];
  const offered = current && !ready.some((d) => d.id === current.id) ? [current, ...ready] : ready;
  const e = (pointer: string) => errors[`${at}${pointer}`];
  return (
    <>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Field label={tr("target.project")} htmlFor={`${id}-project`} error={e("/project_id")}>
          <Select
            id={`${id}-project`}
            value={t.project}
            loading={projects.isPending}
            onChange={(v) => set({ project: v, environment: "", owner: "", item: "", deployment: "" })}
            options={(projects.data?.items ?? []).map((p) => ({ value: p.id, label: p.name }))}
          />
        </Field>
        <Field label={tr("target.where")} htmlFor={`${id}-into`}>
          <Select
            id={`${id}-into`}
            value={t.into}
            onChange={(v) => set({ into: v as Into })}
            options={intos.map((value) => ({ value, label: tr(`target.into.${value}`) }))}
          />
        </Field>
        {t.into === "existing" ? (
          <Field
            label={tr("target.deployment")}
            htmlFor={`${id}-deployment`}
            hint={tr("target.deploymentHint")}
            error={e("/deployment_id")}
            className="sm:col-span-2"
          >
            <Select
              id={`${id}-deployment`}
              value={t.deployment}
              disabled={!t.project}
              loading={items.isLoading || deployments.isFetching}
              onChange={(v) => set({ deployment: v })}
              options={offered.map((d) => ({ value: d.id, label: d.name }))}
            />
          </Field>
        ) : (
          <>
            <Field label={tr("target.environment")} htmlFor={`${id}-env`} error={e("/environment")}>
              <Select
                id={`${id}-env`}
                value={t.environment}
                disabled={!t.project}
                loading={project.isFetching}
                onChange={(v) => set({ environment: v })}
                options={(project.data?.environments ?? []).map((env) => ({ value: env, label: envName(env) }))}
              />
            </Field>
            <Field
              label={tr("target.owner")}
              htmlFor={`${id}-owner`}
              hint={tr("target.ownerHint")}
              error={e("/owner_id")}
            >
              <Select
                id={`${id}-owner`}
                value={t.owner}
                disabled={!t.project}
                loading={members.isFetching}
                onChange={(v) => set({ owner: v })}
                options={(members.data?.items ?? [])
                  .filter(requests)
                  .map((m) => ({ value: m.user.id, label: m.user.name }))}
              />
            </Field>
            <Field
              label={tr("target.item")}
              htmlFor={`${id}-item`}
              hint={tr("target.itemHint")}
              error={e("/catalog_item_id")}
            >
              <Select
                id={`${id}-item`}
                value={t.item}
                disabled={!t.project}
                loading={items.isFetching}
                onChange={(v) => set({ item: v })}
                options={(items.data?.items ?? []).map((i) => ({ value: i.id, label: i.name }))}
              />
            </Field>
            <Field
              label={tr("target.lease")}
              htmlFor={`${id}-lease`}
              hint={tr("target.leaseHint")}
              error={e("/lease_days")}
            >
              <Input
                id={`${id}-lease`}
                type="number"
                min={1}
                value={t.lease}
                onChange={(ev) => set({ lease: ev.target.value })}
              />
            </Field>
            {t.into === "one" && (
              <Field
                label={tr("target.deploymentName")}
                htmlFor={`${id}-deployment-name`}
                error={e("/deployment_name")}
              >
                <Input
                  id={`${id}-deployment-name`}
                  className="font-mono text-secondary"
                  placeholder={nameExample}
                  value={t.name}
                  onChange={(ev) => set({ name: ev.target.value })}
                />
              </Field>
            )}
          </>
        )}
        <Field
          label={tr("target.component")}
          htmlFor={`${id}-component`}
          hint={tr("target.componentHint")}
          error={e("/component")}
        >
          <Input
            id={`${id}-component`}
            className="font-mono text-secondary"
            value={t.component}
            onChange={(ev) => set({ component: ev.target.value })}
          />
        </Field>
      </div>
      {t.into !== "existing" && <InputsEditor t={t} set={set} errors={errors} at={at} id={id} local={local} />}
    </>
  );
}

function InputsEditor({
  t,
  set,
  errors,
  at,
  id,
  local,
}: {
  t: Target;
  set: (patch: Partial<Target>) => void;
  errors: Record<string, string>;
  at: string;
  id: string;
  local: Record<number, string>;
}) {
  const { t: tr } = useTranslation("admin-onboarding");
  const row = (i: number, r: Target["inputs"][number]) => set({ inputs: t.inputs.map((x, j) => (j === i ? r : x)) });
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-1 text-secondary font-medium">{tr("target.inputs")}</legend>
      <p className="text-secondary text-muted">{tr("target.inputsLead")}</p>
      {errors[`${at}/inputs`] && <p className="text-secondary text-bad">{errors[`${at}/inputs`]}</p>}
      {t.inputs.map((r, i) => (
        <div key={i} className="grid grid-cols-[1fr_2fr_auto] items-start gap-2">
          <Field
            label={tr("target.input")}
            htmlFor={`${id}-input-${i}-key`}
            error={local[i] ?? errors[`${at}/inputs/${r.key.trim()}`]}
          >
            <Input
              id={`${id}-input-${i}-key`}
              className="font-mono"
              value={r.key}
              onChange={(ev) => row(i, { ...r, key: ev.target.value })}
            />
          </Field>
          <Field label={tr("target.value")} htmlFor={`${id}-input-${i}-value`}>
            <Input
              id={`${id}-input-${i}-value`}
              value={r.value}
              onChange={(ev) => row(i, { ...r, value: ev.target.value })}
            />
          </Field>
          <Button
            type="button"
            size="icon"
            variant="quiet"
            className="mt-6"
            aria-label={tr("target.removeInput", { name: r.key || String(i + 1) })}
            onClick={() => set({ inputs: t.inputs.filter((_, j) => j !== i) })}
          >
            <X className="size-3.5" />
          </Button>
        </div>
      ))}
      <div>
        <Button
          type="button"
          variant="secondary"
          onClick={() => set({ inputs: [...t.inputs, { key: "", value: "" }] })}
        >
          <Plus className="size-3.5" /> {tr("target.addInput")}
        </Button>
      </div>
    </fieldset>
  );
}
