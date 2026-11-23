import { useMutation, useQuery } from "@tanstack/react-query";
import { Check, Minus } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { q } from "@/lib/query";
import { limitLabel } from "@/features/requests/limits";
import { useEnvName } from "@/lib/environments";
import { i18n } from "@/lib/i18n";
import { day2Name } from "@/lib/i18n/names";
import { quotaLabel, quotaWhere } from "@/features/requests/quota";
import { failed } from "../common";
import { useProjects } from "./shared";

type S = components["schemas"];

const day2Actions = [
  "power_on", "power_off", "shutdown_guest", "restart_guest", "suspend", "reset", "console", "resize", "disk_add", "disk_extend", "disk_remove",
  "tags_edit", "nic_add", "nic_move", "nic_remove", "iso_mount", "iso_unmount", "snapshot_create", "snapshot_revert", "snapshot_delete",
  "nsx_group_add", "nsx_group_remove", "rename", "edit", "change_owner", "change_project", "extend_lease", "vm_unregister", "unregister",
  "delete",
]; // prettier-ignore

/** A built-in day-2 action's name in the language shown, from its key. */
function actionName(key: string): string {
  const en: unknown = i18n.getResource("en", "names", `day2.${key}`);
  return typeof en === "string" ? day2Name({ key, name: en }) : key;
}

const inputsExample = '{"size": "S"}';

function Considered({ list }: { list: S["PolicyConsidered"][] }) {
  const { t } = useTranslation("admin-governance");
  if (list.length === 0) return null;
  return (
    <ul className="mt-1 flex flex-col gap-0.5 text-secondary">
      {list.map((c) => (
        <li key={c.id} className={c.applies ? "flex gap-1.5" : "flex gap-1.5 text-muted"}>
          {c.applies ? (
            <Check className="mt-0.5 size-3.5 shrink-0 text-ok" aria-label={t("policies.explain.applies")} />
          ) : (
            <Minus className="mt-0.5 size-3.5 shrink-0" aria-label={t("policies.explain.notApplies")} />
          )}
          <span>{t("policies.explain.considered", { name: c.name, why: c.why })}</span>
        </li>
      ))}
    </ul>
  );
}

function Decision({ title, d }: { title: string; d?: S["PolicyDecision"] }) {
  const { t } = useTranslation("admin-governance");
  if (!d) return null;
  return (
    <Panel title={title} level={3}>
      <div className="flex flex-col gap-1">
        <p className="text-body">{d.outcome}</p>
        <p className="text-secondary text-muted">
          {t("policies.explain.decidedBy", { by: d.decided_by ?? t("policies.explain.nothing") })}
        </p>
        <Considered list={d.considered} />
      </div>
    </Panel>
  );
}

/** Each deployment limit here and the policy deciding it, with what counts toward a count already (ADR-0047). */
function Limits({ limits }: { limits: S["PolicyLimits"] }) {
  const { t } = useTranslation("admin-governance");
  const envName = useEnvName();
  return (
    <Panel title={t("policies.limits.title")} level={3}>
      <div className="flex flex-col gap-1">
        {limits.lines.length === 0 ? (
          <p className="text-secondary text-muted">{t("policies.explain.noLimit")}</p>
        ) : (
          <ul className="flex flex-col gap-0.5 text-secondary">
            {limits.lines.map((l) => {
              const [name, unit] = limitLabel[l.kind];
              const most = `${l.limit}${unit ? ` ${unit}` : ""}`;
              const soft = l.enforcement === "soft";
              const values = {
                name: l.environment
                  ? t("policies.explain.limitIn", { name, environment: envName(l.environment) })
                  : name,
                used: l.used,
                most,
                policy: l.policy,
              };
              return (
                <li key={l.kind}>
                  {l.used != null
                    ? t(soft ? "policies.explain.limitUsedSoft" : "policies.explain.limitUsed", values)
                    : t(soft ? "policies.explain.limitAtMostSoft" : "policies.explain.limitAtMost", values)}
                </li>
              );
            })}
          </ul>
        )}
        <Considered list={limits.considered} />
      </div>
    </Panel>
  );
}

/** The inputs typed as JSON: undefined when none, null when they aren't an object. */
function parsedInputs(text: string): Record<string, unknown> | undefined | null {
  if (!text.trim()) return undefined;
  try {
    const v: unknown = JSON.parse(text);
    return v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}

export function WhatApplies() {
  const { t } = useTranslation("admin-governance");
  const projects = useProjects();
  const envs = useQuery(q.environments());
  const envName = useEnvName();
  const [f, setF] = useState({ project: "", environment: "", item: "none", action: "none", inputs: "" });
  const [inputsError, setInputsError] = useState<string>();
  // Counts the explains refused for their inputs: each moves the focus to the field, once its note says why.
  const [inputsRefused, setInputsRefused] = useState(0);
  useEffect(() => {
    if (inputsRefused) document.getElementById("ex-inputs")?.focus();
  }, [inputsRefused]);
  const project = f.project || projects.items[0]?.id || "";
  // The items the project may request.
  const items = useQuery({ ...q.catalog({ projectId: project }), enabled: Boolean(project) });
  const environment = f.environment || envs.data?.items[0]?.key || "";
  const explain = useMutation({
    mutationFn: (inputs?: Record<string, unknown>) =>
      call(
        api.POST("/admin/policies/explain", {
          body: {
            project_id: project,
            environment,
            ...(f.item !== "none" ? { item_id: f.item } : {}),
            ...(f.action !== "none" ? { action: f.action } : {}),
            ...(inputs ? { inputs } : {}),
          },
        }),
      ),
    onError: (e) => failed(e, t("policies.explain.failed")),
  });
  const r = explain.data;
  return (
    <div className="flex flex-col gap-4">
      <Panel title={t("policies.explain.title")} description={t("policies.explain.lead")}>
        <form
          className="flex flex-wrap items-end gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            const inputs = parsedInputs(f.inputs);
            setInputsError(
              inputs === null ? t("policies.explain.inputsObject", { example: inputsExample }) : undefined,
            );
            if (inputs === null) setInputsRefused((n) => n + 1);
            else explain.mutate(inputs);
          }}
        >
          <Field label={t("policies.project")} htmlFor="ex-project" className="w-48">
            <Select
              id="ex-project"
              value={project}
              onChange={(v) => setF({ ...f, project: v, environment: "", item: "none" })}
              options={projects.items.map((p) => ({ value: p.id, label: p.name }))}
            />
          </Field>
          <Field label={t("policies.environment")} htmlFor="ex-env" className="w-36">
            <Select
              id="ex-env"
              value={environment}
              onChange={(v) => setF({ ...f, environment: v })}
              options={(envs.data?.items ?? []).map((e) => ({ value: e.key, label: e.name }))}
            />
          </Field>
          <Field label={t("policies.explain.item")} htmlFor="ex-item" className="w-56">
            <Select
              id="ex-item"
              value={f.item}
              onChange={(v) => setF({ ...f, item: v })}
              options={[
                { value: "none", label: t("policies.explain.none") },
                ...(items.data?.items ?? []).map((i) => ({ value: i.id, label: i.name })),
              ]}
            />
          </Field>
          <Field label={t("policies.explain.action")} htmlFor="ex-action" className="w-44">
            <Select
              id="ex-action"
              value={f.action}
              onChange={(v) => setF({ ...f, action: v })}
              options={[
                { value: "none", label: t("policies.explain.none") },
                ...day2Actions.map((a) => ({ value: a, label: actionName(a), meta: a })),
              ]}
            />
          </Field>
          <Field
            label={t("policies.explain.inputs")}
            htmlFor="ex-inputs"
            className="w-56"
            error={inputsError}
            hint={t("policies.explain.inputsHint")}
          >
            <Input
              id="ex-inputs"
              className="font-mono"
              placeholder={inputsExample}
              value={f.inputs}
              onChange={(e) => setF({ ...f, inputs: e.target.value })}
            />
          </Field>
          <Button type="submit" variant="primary" disabled={!project || !environment || explain.isPending}>
            {t("policies.explain.explain")}
          </Button>
        </form>
      </Panel>
      {r && (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2" aria-live="polite">
          <Decision title={t("policies.explain.approval")} d={r.approval} />
          <Decision title={t("policies.explain.day2")} d={r.day2} />
          <Decision title={t("policies.explain.lease")} d={r.lease} />
          <Decision title={t("policies.explain.naming")} d={r.naming} />
          <Panel title={t("policies.explain.quota")} level={3}>
            {r.quota.length === 0 ? (
              <p className="text-secondary text-muted">{t("policies.explain.noLimit")}</p>
            ) : (
              <ul className="flex flex-col gap-0.5 text-secondary">
                {r.quota.map((l) => (
                  <li key={`${l.resource}-${l.level}`}>
                    {t(l.enforcement === "soft" ? "policies.explain.quotaLineSoft" : "policies.explain.quotaLine", {
                      name: quotaLabel[l.resource][0],
                      where: quotaWhere(l, envName) || t("policies.explain.quotaProject"),
                      taken: l.used + l.reserved,
                      limit: l.limit ?? t("policies.explain.quotaNoLimit"),
                    })}
                  </li>
                ))}
              </ul>
            )}
          </Panel>
          <Limits limits={r.limits} />
        </div>
      )}
    </div>
  );
}
