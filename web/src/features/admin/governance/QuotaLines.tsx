// Quota limits by resource alone: each member's in a project, and rvav's,
// as a whole and for each user (ADR-0047).
import { useMutation, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { Gauge } from "lucide-react";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import type { components } from "@/lib/api/schema";
import { errorAt, failed, fieldErrors } from "./common";

type S = components["schemas"];

export const quotaResources: { value: S["QuotaResource"]; label: string }[] = [
  { value: "vcpu", label: "vCPU" },
  { value: "memory_gb", label: "Memory (GB)" },
  { value: "storage_gb", label: "Storage (GB)" },
  { value: "vm_count", label: "VMs" },
];
export const resourceName = (r: S["QuotaResource"]) => quotaResources.find((x) => x.value === r)?.label ?? r;
export const enforcements = [
  { value: "hard", label: "Refused" },
  { value: "soft", label: "Needs approval" },
];

export type QuotaLine = {
  resource: S["QuotaResource"];
  limit?: number | null;
  enforcement: "hard" | "soft";
  set_by_admin?: boolean;
};

export type QuotaWrite = Pick<QuotaLine, "resource" | "enforcement"> & { limit: number | null };

const write = ({ resource, limit, enforcement }: QuotaLine): QuotaWrite => ({
  resource,
  limit: limit ?? null,
  enforcement,
});

function LineSheet({
  title,
  lines,
  editing,
  cap,
  save,
  onClose,
  returnFocus,
}: {
  title: string;
  lines: QuotaLine[];
  /** The resource changed; none for a new limit. */
  editing?: S["QuotaResource"];
  /** The most a limit may be, said under the field. */
  cap?: (r: S["QuotaResource"]) => string | undefined;
  save: ReturnType<typeof useSave>;
  onClose: () => void;
  returnFocus?: () => HTMLElement | null;
}) {
  const existing = lines.find((l) => l.resource === editing);
  const [f, setF] = useState(() => ({
    resource:
      existing?.resource ?? quotaResources.find((r) => !lines.some((l) => l.resource === r.value))?.value ?? "vcpu",
    limit: existing?.limit == null ? "" : String(existing.limit),
    enforcement: existing?.enforcement ?? "hard",
  }));
  const twin = lines.some((l) => l.resource !== editing && l.resource === f.resource);
  const value = Number(f.limit);
  const valid = f.limit.trim() === "" || (Number.isInteger(value) && value >= 0);
  const row: QuotaLine = {
    resource: f.resource,
    limit: f.limit.trim() === "" ? null : value,
    enforcement: f.enforcement,
  };
  const items = existing ? lines.map((l) => (l.resource === editing ? row : l)) : [...lines, row];
  const at = items.indexOf(row);
  const problems = fieldErrors(save.error);
  const own = (field: string) => errorAt(problems, `/items/${at}/${field}`);
  // What the server says about the other lines, or the list as a whole, such as a limit now above a cap.
  const elsewhere = Object.entries(problems).flatMap(([pointer, message]) => {
    if (["resource", "limit", "enforcement"].some((f) => pointer.startsWith(`/items/${at}/${f}`))) return [];
    const i = /^\/items\/(\d+)/.exec(pointer)?.[1];
    const other = i === undefined || i === String(at) ? undefined : items[Number(i)];
    return [other ? `The ${resourceName(other.resource)} limit: ${message}` : message];
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      returnFocus={returnFocus}
      title={title}
      description="Requests that would pass a limit are refused, or need an approval."
      submitLabel={save.isPending ? "Saving…" : "Save limit"}
      submitDisabled={twin || !valid}
      onSubmit={() =>
        save.mutate(items, {
          onSuccess: () => {
            toast.success("Limit saved");
            onClose();
          },
        })
      }
    >
      <Field
        label="Resource"
        htmlFor="line-resource"
        error={twin ? "This resource has a limit already: change that one." : own("resource")}
      >
        <Select
          id="line-resource"
          value={f.resource}
          onChange={(v) => setF({ ...f, resource: v as S["QuotaResource"] })}
          options={quotaResources}
        />
      </Field>
      <Field
        label="Limit"
        htmlFor="line-limit"
        hint={cap?.(f.resource) ?? "Empty for no limit."}
        error={valid ? own("limit") : "A whole number, 0 or more."}
      >
        <Input
          id="line-limit"
          type="number"
          min={0}
          className="w-32"
          value={f.limit}
          onChange={(e) => setF({ ...f, limit: e.target.value })}
        />
      </Field>
      <Field label="When passed" htmlFor="line-enforcement" error={own("enforcement")}>
        <Select
          id="line-enforcement"
          value={f.enforcement}
          onChange={(v) => setF({ ...f, enforcement: v as QuotaLine["enforcement"] })}
          options={enforcements}
        />
      </Field>
      {elsewhere.length > 0 && (
        <div role="alert" className="flex flex-col gap-1 rounded-md bg-bad-soft px-3 py-2 text-secondary text-bad">
          {elsewhere.map((m) => (
            <p key={m}>{m}</p>
          ))}
        </div>
      )}
    </Sheet>
  );
}

function useSave(save: (items: QuotaWrite[]) => Promise<unknown>, invalidates: QueryKey[]) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (items: QuotaLine[]) => save(items.map(write)),
    // The list is read again before the sheet closes, so the focus can go to what replaces the empty state.
    onSuccess: async () => {
      for (const queryKey of [["requests", "preview"], ["deployments"]]) void qc.invalidateQueries({ queryKey });
      await Promise.all(invalidates.map((queryKey) => qc.invalidateQueries({ queryKey })));
    },
    onError: (e) => {
      if (!Object.keys(fieldErrors(e)).length) failed(e, "Not saved");
    },
  });
}

/** A panel of limits, one per resource; saving replaces them all. */
export function QuotaLines({
  title,
  description,
  label,
  empty,
  lines,
  loading,
  error,
  canEdit,
  locked,
  cap,
  save,
  invalidates,
}: {
  title: string;
  description: string;
  label: string;
  empty: string;
  lines: QuotaLine[] | undefined;
  loading: boolean;
  error: React.ReactNode;
  canEdit: boolean;
  /** A line the caller may not change: an administrator set it. */
  locked?: (l: QuotaLine) => boolean;
  cap?: (r: S["QuotaResource"]) => string | undefined;
  save: (items: QuotaWrite[]) => Promise<unknown>;
  invalidates: QueryKey[];
}) {
  const saving = useSave(save, invalidates);
  const [editing, setEditing] = useState<{ resource?: S["QuotaResource"]; fromEmpty?: boolean } | null>(null);
  const addRef = useRef<HTMLButtonElement>(null);
  const [removing, setRemoving] = useState<QuotaLine | null>(null);
  const all = lines ?? [];
  const columns: Column<QuotaLine>[] = [
    {
      id: "resource",
      header: "Resource",
      sortValue: (l) => resourceName(l.resource),
      cell: (l) => (
        <div>
          <span className="font-medium">{resourceName(l.resource)}</span>
          {l.set_by_admin && <div className="text-caption text-faint">Set by an administrator</div>}
        </div>
      ),
    },
    {
      id: "limit",
      header: "Limit",
      align: "end",
      sortValue: (l) => l.limit ?? -1,
      cell: (l) => <span className="tabular-nums">{l.limit ?? "None"}</span>,
    },
    {
      id: "enforcement",
      header: "When passed",
      cell: (l) => <span className="text-secondary">{enforcements.find((e) => e.value === l.enforcement)?.label}</span>,
    },
  ];
  const full = all.length >= quotaResources.length;
  const add = canEdit && !full && (
    <Button
      ref={addRef}
      size="sm"
      variant="primary"
      disabled={!lines}
      onClick={() => {
        saving.reset();
        setEditing({ fromEmpty: all.length === 0 });
      }}
    >
      Add a limit
    </Button>
  );
  return (
    <Panel title={title} description={description} flush actions={all.length ? add : undefined}>
      <DataTable
        label={label}
        columns={columns}
        rows={lines}
        rowKey={(l) => l.resource}
        minWidth={480}
        loading={loading}
        error={error}
        empty={
          <EmptyState
            icon={Gauge}
            title="No limit"
            body={canEdit ? `${empty} Add a limit on vCPU, memory, storage or VMs.` : empty}
            action={add || undefined}
          />
        }
        rowMenuLabel={(l) => `Actions for the ${resourceName(l.resource)} limit`}
        rowMenu={
          canEdit
            ? (l) =>
                locked?.(l) ? null : (
                  <>
                    <MenuItem
                      onSelect={() => {
                        saving.reset();
                        setEditing({ resource: l.resource });
                      }}
                    >
                      Change
                    </MenuItem>
                    <MenuSeparator />
                    <MenuItem danger onSelect={() => setRemoving(l)}>
                      Remove
                    </MenuItem>
                  </>
                )
            : undefined
        }
      />
      {editing !== null && (
        <LineSheet
          title={editing.resource ? "Change the limit" : "New limit"}
          lines={all}
          editing={editing.resource}
          cap={cap}
          save={saving}
          onClose={() => setEditing(null)}
          returnFocus={editing.fromEmpty ? () => addRef.current : undefined}
        />
      )}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Remove the ${resourceName(removing.resource)} limit?`}
          description="Requests are no longer held to it."
          consequences="The other levels' limits still apply."
          confirmLabel="Remove limit"
          pending={saving.isPending}
          onConfirm={() =>
            saving.mutate(
              all.filter((l) => l.resource !== removing.resource),
              {
                onSuccess: () => {
                  toast.success("Limit removed");
                  setRemoving(null);
                },
                // The dialog has no field to show them under.
                onError: (e) => {
                  if (Object.keys(fieldErrors(e)).length) failed(e, "Not removed");
                },
              },
            )
          }
        />
      )}
    </Panel>
  );
}
