import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Tag } from "lucide-react";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { i18n } from "@/lib/i18n";
import { adm } from "@/lib/query";
import { errorAt, failed, fieldErrors, OfferEditor, offersText, writable } from "../common";
import { useProjects } from "./shared";

type S = components["schemas"];
type Policy = S["NamingPolicy"];

/** How a policy counts: "From 1, by 1" and whose counter it is. */
function counterText(p: Pick<Policy, "start" | "increment" | "counter">): string {
  return i18n.t(
    p.counter === "policy"
      ? "admin-governance:policies.naming.countedPolicy"
      : "admin-governance:policies.naming.counted",
    {
      start: p.start,
      increment: p.increment,
    },
  );
}

function NamingSheet({ policy, onClose }: { policy?: Policy; onClose: () => void }) {
  const { t } = useTranslation("admin-governance");
  const qc = useQueryClient();
  const projects = useProjects();
  const [f, setF] = useState<S["NamingPolicyWrite"]>(
    policy
      ? writable(policy, "id")
      : {
          name: "",
          template: "{project}-{environment}-{role}{seq:03}",
          counter: "prefix",
          start: 1,
          increment: 1,
          offers: [],
        },
  );
  // As typed: a number box cleared is NaN, which JSON sends as null.
  const [counts, setCounts] = useState({ start: String(f.start ?? 1), increment: String(f.increment ?? 1) });
  const [sample, setSample] = useState({ project: "", environment: "dev", role: "app" });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const counted = () => {
    const problems: Record<string, string> = {};
    for (const k of ["start", "increment"] as const)
      if (!/^\d+$/.test(counts[k].trim())) problems[`/${k}`] = t("policies.naming.wholeNumber");
    setErrors(problems);
    return Object.keys(problems).length === 0;
  };
  const body = { ...f, start: Number(counts.start.trim()), increment: Number(counts.increment.trim()) };
  const preview = useMutation({
    mutationFn: () =>
      call(
        api.POST("/admin/naming-preview", {
          body: {
            template: f.template,
            counter: f.counter,
            start: body.start,
            increment: body.increment,
            policy_id: policy?.id,
            project_id: sample.project || projects.items[0]?.id || "",
            environment: sample.environment,
            inputs: { role: sample.role },
            count: 3,
          },
        }),
      ),
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("policies.naming.noPreview"));
    },
  });
  const save = useMutation({
    mutationFn: () =>
      policy
        ? call(api.PUT("/admin/naming-policies/{id}", { params: { path: { id: policy.id } }, body }))
        : call(api.POST("/admin/naming-policies", { body })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "naming"] });
      toast.success(t("policies.saved", { name: f.name }));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("policies.notSaved"));
    },
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={policy ? t("policies.changeNamed", { name: policy.name }) : t("policies.naming.new")}
      description={t("policies.naming.sheetLead")}
      submitLabel={save.isPending ? t("policies.saving") : t("policies.naming.save")}
      onSubmit={() => {
        if (counted()) save.mutate();
      }}
    >
      <Field label={t("policies.name")} htmlFor="nm-name" error={errors["/name"]}>
        <Input id="nm-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
      </Field>
      <Field
        label={t("policies.naming.template")}
        htmlFor="nm-template"
        error={errors["/template"]}
        hint={t("policies.naming.templateHint", {
          fields: "{project}, {environment}, {role}",
          input: "{inputs.x}",
          seq: "{seq:03}",
        })}
      >
        <Input
          id="nm-template"
          className="font-mono text-secondary"
          value={f.template}
          onChange={(e) => setF({ ...f, template: e.target.value })}
        />
      </Field>
      <Field label={t("policies.naming.counter")} htmlFor="nm-counter">
        <Select
          id="nm-counter"
          value={f.counter}
          onChange={(v) => setF({ ...f, counter: v as Policy["counter"] })}
          options={[
            { value: "prefix", label: t("policies.naming.counterPrefix", { examples: "web-prd-app, web-prd-db" }) },
            { value: "policy", label: t("policies.naming.counterPolicy") },
          ]}
        />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field
          label={t("policies.naming.start")}
          htmlFor="nm-start"
          error={errors["/start"]}
          hint={t("policies.naming.startHint")}
        >
          <Input
            id="nm-start"
            type="number"
            min={0}
            value={counts.start}
            onChange={(e) => setCounts({ ...counts, start: e.target.value })}
          />
        </Field>
        <Field label={t("policies.naming.increment")} htmlFor="nm-increment" error={errors["/increment"]}>
          <Input
            id="nm-increment"
            type="number"
            min={1}
            max={1000}
            value={counts.increment}
            onChange={(e) => setCounts({ ...counts, increment: e.target.value })}
          />
        </Field>
      </div>
      <OfferEditor
        value={f.offers ?? []}
        onChange={(offers) => setF({ ...f, offers })}
        error={errorAt(errors, "/offers")}
      />
      <fieldset className="flex flex-col gap-2 rounded-md border border-line p-3">
        <legend className="px-1 text-secondary font-medium">{t("policies.naming.nextNames")}</legend>
        <div className="grid grid-cols-3 gap-2">
          <Select
            aria-label={t("policies.naming.previewProject")}
            value={sample.project || projects.items[0]?.id || ""}
            onChange={(v) => setSample({ ...sample, project: v })}
            options={projects.items.map((p) => ({ value: p.id, label: p.name }))}
          />
          <Input
            aria-label={t("policies.naming.previewEnvironment")}
            value={sample.environment}
            onChange={(e) => setSample({ ...sample, environment: e.target.value })}
          />
          <Input
            aria-label={t("policies.naming.previewRole")}
            value={sample.role}
            onChange={(e) => setSample({ ...sample, role: e.target.value })}
          />
        </div>
        <div>
          <Button type="button" size="sm" disabled={preview.isPending} onClick={() => counted() && preview.mutate()}>
            {t("policies.naming.preview")}
          </Button>
        </div>
        {preview.data && (
          <div className="text-secondary" aria-live="polite">
            {preview.data.problem && <p className="text-warn">{preview.data.problem}</p>}
            {preview.data.names.length > 0 && (
              <p>
                <Trans
                  t={t}
                  i18nKey="policies.naming.next"
                  values={{ names: preview.data.names.join(", ") }}
                  components={{ mono: <span className="font-mono" /> }}
                />
              </p>
            )}
            {preview.data.skipped.map((s) => (
              <p key={s.name} className="text-muted">
                <Trans
                  t={t}
                  i18nKey="policies.naming.skips"
                  values={{ name: s.name, where: s.taken_in }}
                  components={{ mono: <span className="font-mono" /> }}
                />
              </p>
            ))}
          </div>
        )}
      </fieldset>
    </Sheet>
  );
}

export function NamingPolicies({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation("admin-governance");
  const qc = useQueryClient();
  const policies = useQuery(adm.naming());
  const projects = useProjects();
  const [editing, setEditing] = useState<Policy | "new" | null>(null);
  const [removing, setRemoving] = useState<Policy | null>(null);
  const remove = useMutation({
    mutationFn: (p: Policy) => call(api.DELETE("/admin/naming-policies/{id}", { params: { path: { id: p.id } } })),
    onSuccess: (_, p) => {
      void qc.invalidateQueries({ queryKey: ["admin", "naming"] });
      toast.success(t("policies.removed", { name: p.name }));
    },
    onError: (e) => failed(e, t("policies.notRemoved")),
  });
  const items = policies.data?.items;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      {t("policies.naming.new")}
    </Button>
  );
  const columns: Column<Policy>[] = [
    {
      id: "policy",
      header: t("policies.policy"),
      sortValue: (p) => p.name,
      cell: (p) => <span className="font-medium">{p.name}</span>,
    },
    {
      id: "template",
      header: t("policies.naming.template"),
      cell: (p) => <span className="font-mono text-secondary">{p.template}</span>,
    },
    {
      id: "counter",
      header: t("policies.naming.counter"),
      cell: (p) => <span className="text-secondary">{counterText(p)}</span>,
    },
    {
      id: "offers",
      header: t("policies.naming.offeredTo"),
      cell: (p) => <span className="text-secondary text-muted">{offersText(p.offers, projects.name)}</span>,
    },
  ];
  return (
    <Panel
      title={t("policies.naming.title")}
      description={t("policies.naming.lead")}
      flush
      actions={canEdit && Boolean(items?.length) && add}
    >
      <DataTable
        label={t("policies.naming.title")}
        columns={columns}
        rows={items}
        rowKey={(p) => p.id}
        minWidth={640}
        loading={policies.isPending}
        error={policies.isError && <ErrorState error={policies.error} onRetry={() => void policies.refetch()} />}
        empty={
          <EmptyState
            icon={Tag}
            title={t("policies.naming.emptyTitle")}
            body={t("policies.naming.emptyBody")}
            action={canEdit && add}
          />
        }
        rowMenuLabel={(p) => t("policies.actionsFor", { name: p.name })}
        rowMenu={
          canEdit
            ? (p) => (
                <>
                  <MenuItem onSelect={() => setEditing(p)}>{t("policies.menu.change")}</MenuItem>
                  <MenuSeparator />
                  <MenuItem danger onSelect={() => setRemoving(p)}>
                    {t("policies.menu.remove")}
                  </MenuItem>
                </>
              )
            : undefined
        }
      />
      {editing && <NamingSheet policy={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={t("policies.removeNamed", { name: removing.name })}
          description={
            <Trans
              t={t}
              i18nKey="policies.naming.removeLead"
              values={{ template: removing.template }}
              components={{ mono: <span className="font-mono" /> }}
            />
          }
          consequences={t("policies.naming.removeConsequences", { offers: offersText(removing.offers, projects.name) })}
          confirmLabel={t("policies.naming.removeConfirm")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
