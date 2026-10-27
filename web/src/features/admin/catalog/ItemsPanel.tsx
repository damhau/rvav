import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { ItemIcon, itemTypeLabel } from "@/components/common/misc";
import { EmptyState, Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { api, call, isApiError } from "@/lib/api/client";
import type { AdminCatalogItem, CatalogItemType } from "@/lib/api/types";
import { keys, q, type AdminItemFilters } from "@/lib/query";

const th = "border-b border-line px-3 py-2 font-medium";
const td = "border-b border-line px-3 py-2 align-top";

function fieldErrors(e: unknown): Record<string, string> {
  if (!isApiError(e)) return {};
  return Object.fromEntries(e.fieldErrors.map((f) => [f.field.replace(/^\//, ""), f.message]));
}

function Status({ it }: { it: AdminCatalogItem }) {
  return (
    <div className="flex flex-wrap gap-1">
      {it.published_version ? (
        <Badge tone="ok">Published {it.published_version.number}</Badge>
      ) : (
        <Badge tone="neutral">Not published</Badge>
      )}
      {it.draft_version && <Badge tone="accent">Draft {it.draft_version.number}</Badge>}
    </div>
  );
}

function NewItemDialog({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const categories = useQuery(q.adminCatalogCategories());
  const [form, setForm] = useState({
    slug: "",
    name: "",
    summary: "",
    category: "compute",
    type: "vm_blueprint" as CatalogItemType,
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const create = useMutation({
    mutationFn: () => call(api.POST("/admin/catalog/items", { body: form })),
    onSuccess: (it) => {
      void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
      toast.success(`${it.name} created, with a draft 1.0`);
      void navigate({ to: "/admin/catalog-items/$itemId", params: { itemId: it.slug } });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (isApiError(e) && e.status === 409) toast.error(e.problem.detail ?? e.problem.title);
    },
  });
  const set = (k: keyof typeof form) => (v: string) => setForm((f) => ({ ...f, [k]: v }));
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title="New item"
        description="It starts as a draft; publish it when its form and blueprint are ready."
      >
        <form
          className="flex flex-col gap-3 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            setErrors({});
            create.mutate();
          }}
        >
          <Field label="Name" htmlFor="new-item-name" hint={errors.name}>
            <Input id="new-item-name" value={form.name} onChange={(e) => set("name")(e.target.value)} maxLength={80} />
          </Field>
          <Field
            label="Slug"
            htmlFor="new-item-slug"
            hint={errors.slug ?? "In URLs and YAML; lower case, digits and dashes."}
          >
            <Input
              id="new-item-slug"
              value={form.slug}
              onChange={(e) => set("slug")(e.target.value)}
              aria-invalid={errors.slug ? true : undefined}
              className="font-mono"
            />
          </Field>
          <Field label="Summary" htmlFor="new-item-summary" hint={errors.summary}>
            <Textarea
              id="new-item-summary"
              value={form.summary}
              onChange={(e) => set("summary")(e.target.value)}
              maxLength={200}
            />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Category" htmlFor="new-item-category">
              <Select
                id="new-item-category"
                value={form.category}
                onChange={set("category")}
                options={(categories.data?.items ?? []).map((c) => ({ value: c.key, label: c.name }))}
              />
            </Field>
            <Field label="Type" htmlFor="new-item-type">
              <Select
                id="new-item-type"
                value={form.type}
                onChange={set("type")}
                options={(["vm_blueprint", "awx_job_template", "awx_workflow"] as const).map((t) => ({
                  value: t,
                  label: itemTypeLabel[t],
                }))}
              />
            </Field>
          </div>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button
              type="submit"
              variant="primary"
              disabled={create.isPending || !form.name || !form.slug || !form.summary}
            >
              {create.isPending ? "Creating…" : "Create"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function ImportDialog({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [text, setText] = useState("");
  const [problems, setProblems] = useState<string[]>([]);
  const run = useMutation({
    mutationFn: () =>
      call(
        api.POST("/admin/catalog/import", {
          body: text,
          bodySerializer: (b: string) => b,
          headers: { "Content-Type": "application/yaml" },
        }),
      ),
    onSuccess: (out) => {
      void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
      toast.success("Imported as a draft");
      void navigate({ to: "/admin/catalog-items/$itemId", params: { itemId: out.item_id } });
    },
    onError: (e) =>
      setProblems(
        isApiError(e) && e.fieldErrors.length
          ? e.fieldErrors.map((x) => x.message)
          : [isApiError(e) ? (e.problem.detail ?? e.problem.title) : "The import failed."],
      ),
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title="Import an item"
        description="Paste or open an item exported as YAML. It comes in as a draft; nothing is published."
        className="w-[min(640px,calc(100vw-32px))]"
      >
        <div className="flex flex-col gap-3 p-4">
          <input
            type="file"
            accept=".yaml,.yml,application/yaml,text/yaml"
            aria-label="YAML file"
            className="text-xs"
            onChange={async (e) => setText((await e.target.files?.[0]?.text()) ?? "")}
          />
          <Textarea
            aria-label="YAML"
            className="min-h-64 font-mono text-xs"
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder="apiVersion: rvav.dev/v1"
          />
          {problems.length > 0 && (
            <ul aria-label="Import problems" className="flex flex-col gap-1 text-xs text-bad">
              {problems.map((p) => (
                <li key={p}>{p}</li>
              ))}
            </ul>
          )}
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button variant="primary" disabled={!text.trim() || run.isPending} onClick={() => run.mutate()}>
              {run.isPending ? "Importing…" : "Import"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

export function ItemsPanel() {
  const [filters, setFilters] = useState<AdminItemFilters>({});
  const [dialog, setDialog] = useState<"new" | "import" | null>(null);
  const items = useQuery(q.adminCatalogItems(filters));
  const categories = useQuery(q.adminCatalogCategories());
  const list = items.data?.items ?? [];
  return (
    <Panel
      title="Items"
      flush
      actions={
        <div className="flex gap-2">
          <Button size="sm" onClick={() => setDialog("import")}>
            Import YAML
          </Button>
          <Button size="sm" variant="primary" onClick={() => setDialog("new")}>
            New item
          </Button>
        </div>
      }
    >
      <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2">
        <Input
          aria-label="Search items"
          placeholder="Search"
          className="w-56"
          value={filters.q ?? ""}
          onChange={(e) => setFilters((f) => ({ ...f, q: e.target.value || undefined }))}
        />
        <Select
          aria-label="Category"
          value={filters.category ?? "any"}
          onChange={(v) => setFilters((f) => ({ ...f, category: v === "any" ? undefined : v }))}
          options={[
            { value: "any", label: "All categories" },
            ...(categories.data?.items ?? []).map((c) => ({ value: c.key, label: c.name })),
          ]}
          className="w-44"
        />
        <Select
          aria-label="State"
          value={filters.state ?? "any"}
          onChange={(v) =>
            setFilters((f) => ({ ...f, state: v === "any" ? undefined : (v as AdminItemFilters["state"]) }))
          }
          options={[
            { value: "any", label: "Any state" },
            { value: "published", label: "Published" },
            { value: "unpublished", label: "Not published" },
          ]}
          className="w-40"
        />
      </div>
      {items.isPending && <SkeletonRows rows={5} cols={5} />}
      {items.isError && <ErrorState error={items.error} onRetry={() => void items.refetch()} />}
      {items.data && list.length === 0 && <EmptyState title="No items" body="Nothing matches these filters." />}
      {list.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[820px] border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  Item
                </th>
                <th scope="col" className={th}>
                  Type
                </th>
                <th scope="col" className={th}>
                  Versions
                </th>
                <th scope="col" className={th}>
                  Open to
                </th>
              </tr>
            </thead>
            <tbody>
              {list.map((it) => (
                <tr key={it.id} className="hover:bg-sunken">
                  <td className={`${td} pl-4`}>
                    <div className="flex items-start gap-2.5">
                      <ItemIcon name={it.icon} />
                      <div className="min-w-0">
                        <Link
                          to="/admin/catalog-items/$itemId"
                          params={{ itemId: it.slug }}
                          className="font-medium text-fg hover:text-accent"
                        >
                          {it.name}
                        </Link>
                        <div className="font-mono text-[11px] text-faint">{it.slug}</div>
                      </div>
                    </div>
                  </td>
                  <td className={`${td} text-xs text-muted`}>
                    {itemTypeLabel[it.type]}
                    {it.source.kind === "awx" && <div className="text-[11px] text-faint">from AWX</div>}
                  </td>
                  <td className={td}>
                    <Status it={it} />
                  </td>
                  <td className={`${td} text-xs text-muted`}>
                    {it.entitlements.all_projects
                      ? "Every project"
                      : it.entitlements.project_ids.length === 0
                        ? "No project"
                        : `${it.entitlements.project_ids.length} project${it.entitlements.project_ids.length > 1 ? "s" : ""}`}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {dialog === "new" && <NewItemDialog onClose={() => setDialog(null)} />}
      {dialog === "import" && <ImportDialog onClose={() => setDialog(null)} />}
    </Panel>
  );
}
