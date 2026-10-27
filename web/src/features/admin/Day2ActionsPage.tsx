import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input, Textarea } from "@/components/ui/input";
import { api, call, isApiError } from "@/lib/api/client";
import type { AwxAction, AwxActionUpdate } from "@/lib/api/types";
import { keys, q } from "@/lib/query";
import { relative } from "@/lib/utils";

type Target = AwxAction["types"][number];

const targetLabel: Record<Target, string> = { "vsphere.vm": "VMs", deployment: "Deployments" };

function Status({ a }: { a: AwxAction }) {
  if (a.gone) return <Badge tone="neutral">Not in AWX</Badge>;
  if (!a.enabled) return <Badge tone="neutral">Off</Badge>;
  if (a.warnings.length > 0) return <Badge tone="warn">Check settings</Badge>;
  return <Badge tone="ok">Offered</Badge>;
}

function EditDialog({ a, onClose }: { a: AwxAction; onClose: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState(a.name);
  const [types, setTypes] = useState<Target[]>(a.types);
  const [condition, setCondition] = useState(a.condition);
  const [enabled, setEnabled] = useState(a.enabled);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: (body: AwxActionUpdate) =>
      call(api.PATCH("/admin/awx/actions/{key}", { params: { path: { key: a.key } }, body })),
    onSuccess: (saved) => {
      qc.setQueryData(keys.awxActions, (old: { items: AwxAction[] } | undefined) =>
        old ? { items: old.items.map((x) => (x.key === saved.key ? saved : x)) } : old,
      );
      toast.success(`${saved.name} saved`);
      onClose();
    },
    onError: (e) => {
      if (isApiError(e) && e.fieldErrors.length > 0) {
        setErrors(Object.fromEntries(e.fieldErrors.map((f) => [f.field.replace(/^\//, ""), f.message])));
        return;
      }
      toast.error(isApiError(e) ? e.problem.title : "Saving failed", {
        description: isApiError(e) ? e.problem.detail : undefined,
      });
    },
  });
  const toggle = (t: Target, on: boolean) => setTypes((cur) => (on ? [...cur, t] : cur.filter((x) => x !== t)));
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        title={`Edit ${a.name}`}
        description={
          <>
            AWX {a.workflow ? "workflow" : "job template"} <span className="font-mono">{a.template}</span>
          </>
        }
        className="w-[min(560px,calc(100vw-32px))]"
      >
        <form
          className="flex flex-col gap-4 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            setErrors({});
            save.mutate({ name, types, condition, enabled });
          }}
        >
          <Field label="Name" htmlFor="awx-action-name">
            <Input
              id="awx-action-name"
              value={name}
              maxLength={80}
              onChange={(e) => setName(e.target.value)}
              aria-invalid={errors.name ? true : undefined}
              aria-describedby={errors.name ? "awx-action-name-error" : undefined}
            />
            {errors.name && (
              <p id="awx-action-name-error" className="text-xs text-bad">
                {errors.name}
              </p>
            )}
          </Field>
          <fieldset
            className="flex flex-col gap-1.5"
            aria-describedby={errors.types ? "awx-action-types-error" : undefined}
          >
            <legend className="mb-1 text-xs font-medium">Runs on</legend>
            {(Object.keys(targetLabel) as Target[]).map((t) => (
              <div key={t} className="flex items-center gap-2 text-[13px]">
                <Checkbox id={`awx-action-${t}`} checked={types.includes(t)} onChange={(on) => toggle(t, on)} />
                <label htmlFor={`awx-action-${t}`}>{targetLabel[t]}</label>
                <span className="text-xs text-faint">
                  {t === "deployment" ? "every VM of the deployment at once" : "one VM at a time"}
                </span>
              </div>
            ))}
            {errors.types && (
              <p id="awx-action-types-error" className="text-xs text-bad">
                {errors.types}
              </p>
            )}
          </fieldset>
          <Field
            label="Condition"
            htmlFor="awx-action-condition"
            hint={
              <>
                Offered only where this CEL expression is true, for example{" "}
                <span className="font-mono">resource.os_family == &apos;linux&apos;</span>. Empty: everywhere.
              </>
            }
          >
            <Textarea
              id="awx-action-condition"
              className="font-mono text-xs"
              value={condition}
              onChange={(e) => setCondition(e.target.value)}
              aria-invalid={errors.condition ? true : undefined}
              aria-describedby={errors.condition ? "awx-action-condition-error" : undefined}
            />
            {errors.condition && (
              <p id="awx-action-condition-error" className="text-xs text-bad">
                {errors.condition}
              </p>
            )}
          </Field>
          <div className="flex items-center gap-2 text-[13px]">
            <Checkbox id="awx-action-enabled" checked={enabled} onChange={setEnabled} />
            <label htmlFor="awx-action-enabled">Offered on deployments</label>
          </div>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button
              type="submit"
              variant="primary"
              disabled={save.isPending || types.length === 0 || name.trim() === ""}
            >
              {save.isPending ? "Saving…" : "Save"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Row({ a, onEdit }: { a: AwxAction; onEdit: () => void }) {
  return (
    <tr className="hover:bg-sunken">
      <td className="border-b border-line px-4 py-2 align-top">
        <div className="font-medium">{a.name}</div>
        <div className="font-mono text-[11px] text-faint">{a.key}</div>
      </td>
      <td className="border-b border-line px-3 py-2 align-top">
        <div className="font-mono text-xs">{a.template}</div>
        <div className="text-[11px] text-faint">
          {a.workflow ? "Workflow" : "Job template"} #{a.template_id}
          {a.fields && a.fields.length > 0 ? ` · ${a.fields.join(", ")}` : " · no survey"}
        </div>
      </td>
      <td className="border-b border-line px-3 py-2 align-top text-xs">
        <div>{a.types.map((t) => targetLabel[t]).join(", ")}</div>
        <div className="mt-0.5 font-mono text-[11px] text-muted">{a.condition || "Everywhere"}</div>
      </td>
      <td className="border-b border-line px-3 py-2 align-top">
        <Status a={a} />
        {a.warnings.length > 0 && (
          <ul aria-label={`Warnings for ${a.name}`} className="mt-1 flex flex-col gap-0.5 text-xs text-muted">
            {a.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        )}
      </td>
      <td className="border-b border-line px-4 py-2 text-right align-top text-xs text-faint">
        <div>{relative(a.synced_at)}</div>
        <Button size="sm" variant="ghost" className="mt-1" onClick={onEdit} aria-label={`Edit ${a.name}`}>
          Edit
        </Button>
      </td>
    </tr>
  );
}

export function Day2ActionsPage() {
  const qc = useQueryClient();
  const actions = useQuery(q.awxActions());
  const [editing, setEditing] = useState<AwxAction | null>(null);
  const sync = useMutation({
    mutationFn: () => call(api.POST("/admin/awx/templates/sync")),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.awxActions });
      void qc.invalidateQueries({ queryKey: keys.awxTemplates });
      toast.success("AWX templates synced");
    },
    onError: (e) =>
      toast.error(isApiError(e) ? e.problem.title : "The sync failed", {
        description: isApiError(e) ? e.problem.detail : undefined,
      }),
  });
  const items = actions.data?.items ?? [];
  return (
    <Page>
      <PageHeader
        title="Day-2 actions"
        meta="The native actions are built in; who may run which action is set by the day-2 policies."
      />
      <Panel
        title="From AWX"
        flush
        actions={
          <Button size="sm" disabled={sync.isPending} onClick={() => sync.mutate()}>
            {sync.isPending ? "Syncing…" : "Sync now"}
          </Button>
        }
      >
        {actions.isPending && <SkeletonRows rows={3} cols={5} />}
        {actions.isError && <ErrorState error={actions.error} onRetry={() => void actions.refetch()} />}
        {actions.data && items.length === 0 && (
          <EmptyState
            title="No AWX actions yet"
            body="Templates of rvav's AWX organisation with the day2 label become day-2 actions here, every few minutes or when you sync."
          />
        )}
        {items.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[880px] border-collapse text-[13px]">
              <thead>
                <tr className="label text-left">
                  <th scope="col" className="border-b border-line px-4 py-2 font-medium">
                    Action
                  </th>
                  <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                    AWX template
                  </th>
                  <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                    Offered on
                  </th>
                  <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                    Status
                  </th>
                  <th scope="col" className="border-b border-line px-4 py-2 text-right font-medium">
                    Synced
                  </th>
                </tr>
              </thead>
              <tbody>
                {items.map((a) => (
                  <Row key={a.key} a={a} onEdit={() => setEditing(a)} />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
      {editing && <EditDialog a={editing} onClose={() => setEditing(null)} />}
    </Page>
  );
}
