import { useMutation, useQuery } from "@tanstack/react-query";
import { Check, Minus } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { q } from "@/lib/query";
import { limitLabel, useEnvName } from "@/features/requests/limits";
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

function Considered({ list }: { list: S["PolicyConsidered"][] }) {
  if (list.length === 0) return null;
  return (
    <ul className="mt-1 flex flex-col gap-0.5 text-secondary">
      {list.map((c) => (
        <li key={c.id} className={c.applies ? "flex gap-1.5" : "flex gap-1.5 text-muted"}>
          {c.applies ? (
            <Check className="mt-0.5 size-3.5 shrink-0 text-ok" aria-label="Applies" />
          ) : (
            <Minus className="mt-0.5 size-3.5 shrink-0" aria-label="Doesn't apply" />
          )}
          <span>
            {c.name}: {c.why}
          </span>
        </li>
      ))}
    </ul>
  );
}

function Decision({ title, d }: { title: string; d?: S["PolicyDecision"] }) {
  if (!d) return null;
  return (
    <Panel title={title} level={3}>
      <div className="flex flex-col gap-1">
        <p className="text-body">{d.outcome}</p>
        <p className="text-secondary text-muted">Decided by: {d.decided_by ?? "nothing"}</p>
        <Considered list={d.considered} />
      </div>
    </Panel>
  );
}

/** Each deployment limit here and the policy deciding it, with what counts toward a count already (ADR-0047). */
function Limits({ limits }: { limits: S["PolicyLimits"] }) {
  const envName = useEnvName();
  return (
    <Panel title="Deployment limits" level={3}>
      <div className="flex flex-col gap-1">
        {limits.lines.length === 0 ? (
          <p className="text-secondary text-muted">No limit.</p>
        ) : (
          <ul className="flex flex-col gap-0.5 text-secondary">
            {limits.lines.map((l) => {
              const [name, unit] = limitLabel[l.kind];
              const most = `${l.limit}${unit ? ` ${unit}` : ""}`;
              return (
                <li key={l.kind}>
                  {name}
                  {l.environment ? ` in ${envName(l.environment)}` : ""}:{" "}
                  {l.used != null ? `${l.used} of ${most}` : `at most ${most}`}, by {l.policy}
                  {l.enforcement === "soft" ? ", which warns" : ""}
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
  const projects = useProjects();
  const envs = useQuery(q.environments());
  const [f, setF] = useState({ project: "", environment: "", item: "none", action: "none", inputs: "" });
  const [inputsError, setInputsError] = useState<string>();
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
    onError: (e) => failed(e, "Couldn't explain"),
  });
  const r = explain.data;
  return (
    <div className="flex flex-col gap-4">
      <Panel
        title="What applies here"
        description="Which policies a request or a day-2 action would meet, and why. Nothing is changed."
      >
        <form
          className="flex flex-wrap items-end gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            const inputs = parsedInputs(f.inputs);
            setInputsError(inputs === null ? 'An object, such as {"size": "S"}.' : undefined);
            if (inputs !== null) explain.mutate(inputs);
          }}
        >
          <Field label="Project" htmlFor="ex-project" className="w-48">
            <Select
              id="ex-project"
              value={project}
              onChange={(v) => setF({ ...f, project: v, environment: "", item: "none" })}
              options={projects.items.map((p) => ({ value: p.id, label: p.name }))}
            />
          </Field>
          <Field label="Environment" htmlFor="ex-env" className="w-36">
            <Select
              id="ex-env"
              value={environment}
              onChange={(v) => setF({ ...f, environment: v })}
              options={(envs.data?.items ?? []).map((e) => ({ value: e.key, label: e.name }))}
            />
          </Field>
          <Field label="Catalog item" htmlFor="ex-item" className="w-56">
            <Select
              id="ex-item"
              value={f.item}
              onChange={(v) => setF({ ...f, item: v })}
              options={[
                { value: "none", label: "None" },
                ...(items.data?.items ?? []).map((i) => ({ value: i.id, label: i.name })),
              ]}
            />
          </Field>
          <Field label="Day-2 action" htmlFor="ex-action" className="w-44">
            <Select
              id="ex-action"
              value={f.action}
              onChange={(v) => setF({ ...f, action: v })}
              options={[{ value: "none", label: "None" }, ...day2Actions.map((a) => ({ value: a, label: a }))]}
            />
          </Field>
          <Field
            label="Inputs (JSON)"
            htmlFor="ex-inputs"
            className="w-56"
            error={inputsError}
            hint="What lease rules and approval conditions read"
          >
            <Input
              id="ex-inputs"
              className="font-mono"
              placeholder='{"size": "S"}'
              value={f.inputs}
              onChange={(e) => setF({ ...f, inputs: e.target.value })}
            />
          </Field>
          <Button type="submit" variant="primary" disabled={!project || !environment || explain.isPending}>
            Explain
          </Button>
        </form>
      </Panel>
      {r && (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2" aria-live="polite">
          <Decision title="Approval" d={r.approval} />
          <Decision title="Day-2" d={r.day2} />
          <Decision title="Lease" d={r.lease} />
          <Decision title="Naming" d={r.naming} />
          <Panel title="Quota" level={3}>
            {r.quota.length === 0 ? (
              <p className="text-secondary text-muted">No limit.</p>
            ) : (
              <ul className="flex flex-col gap-0.5 text-secondary">
                {r.quota.map((l) => (
                  <li key={`${l.resource}-${l.level}`}>
                    {quotaLabel[l.resource][0]}
                    {quotaWhere(l) || " · the project"}: {l.used + l.reserved} of {l.limit ?? "no limit"}
                    {l.enforcement === "soft" ? ", then approval" : ""}
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
