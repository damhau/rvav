import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Panel } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, call, isApiError } from "@/lib/api/client";
import type { AdminCatalogCategory } from "@/lib/api/types";
import { keys, q } from "@/lib/query";

function failed(e: unknown, what: string) {
  toast.error(isApiError(e) ? (e.fieldErrors[0]?.message ?? e.problem.detail ?? e.problem.title) : what);
}

function Row({ c }: { c: AdminCatalogCategory }) {
  const qc = useQueryClient();
  const [name, setName] = useState(c.name);
  const [order, setOrder] = useState(String(c.sort_order ?? 0));
  const refresh = () => void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
  const save = useMutation({
    mutationFn: () =>
      call(
        api.PATCH("/admin/catalog/categories/{key}", {
          params: { path: { key: c.key } },
          body: { name, sort_order: Number(order) || 0 },
        }),
      ),
    onSuccess: () => {
      refresh();
      toast.success(`${name} saved`);
    },
    onError: (e) => failed(e, "Saving failed"),
  });
  const remove = useMutation({
    mutationFn: () => call(api.DELETE("/admin/catalog/categories/{key}", { params: { path: { key: c.key } } })),
    onSuccess: refresh,
    onError: (e) => failed(e, "Removing failed"),
  });
  const changed = name !== c.name || order !== String(c.sort_order ?? 0);
  return (
    <tr className="hover:bg-sunken">
      <td className="border-b border-line px-4 py-1.5 font-mono text-xs">{c.key}</td>
      <td className="border-b border-line px-3 py-1.5">
        <Input aria-label={`Name of ${c.key}`} value={name} onChange={(e) => setName(e.target.value)} className="h-7" />
      </td>
      <td className="border-b border-line px-3 py-1.5">
        <Input
          aria-label={`Order of ${c.key}`}
          type="number"
          value={order}
          onChange={(e) => setOrder(e.target.value)}
          className="h-7 w-20"
        />
      </td>
      <td className="border-b border-line px-3 py-1.5 text-xs text-muted">{c.items ?? 0}</td>
      <td className="border-b border-line px-4 py-1.5 text-right whitespace-nowrap">
        <Button size="sm" variant="ghost" disabled={!changed || save.isPending} onClick={() => save.mutate()}>
          Save
        </Button>
        <Button
          size="sm"
          variant="ghost"
          disabled={(c.items ?? 0) > 0 || remove.isPending}
          title={(c.items ?? 0) > 0 ? "Move its items to another category first" : undefined}
          onClick={() => remove.mutate()}
          aria-label={`Remove ${c.key}`}
        >
          Remove
        </Button>
      </td>
    </tr>
  );
}

export function CategoriesPanel() {
  const qc = useQueryClient();
  const categories = useQuery(q.adminCatalogCategories());
  const [key, setKey] = useState("");
  const [name, setName] = useState("");
  const add = useMutation({
    mutationFn: () => call(api.POST("/admin/catalog/categories", { body: { key, name } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
      toast.success(`${name} added`);
      setKey("");
      setName("");
    },
    onError: (e) => failed(e, "Adding failed"),
  });
  return (
    <Panel title="Categories" flush>
      {categories.isPending && <SkeletonRows rows={3} cols={4} />}
      {categories.isError && <ErrorState error={categories.error} onRetry={() => void categories.refetch()} />}
      {categories.data && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[620px] border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className="border-b border-line px-4 py-2 font-medium">
                  Key
                </th>
                <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                  Name
                </th>
                <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                  Order
                </th>
                <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                  Items
                </th>
                <th scope="col" className="border-b border-line px-4 py-2">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {categories.data.items.map((c) => (
                <Row key={c.key} c={c} />
              ))}
            </tbody>
          </table>
        </div>
      )}
      <form
        className="flex flex-wrap items-center gap-2 px-4 py-3"
        onSubmit={(e) => {
          e.preventDefault();
          add.mutate();
        }}
      >
        <Input
          aria-label="New category key"
          placeholder="key"
          className="h-7 w-36 font-mono"
          value={key}
          onChange={(e) => setKey(e.target.value)}
        />
        <Input
          aria-label="New category name"
          placeholder="Name"
          className="h-7 w-56"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <Button size="sm" type="submit" disabled={!key || !name || add.isPending}>
          Add category
        </Button>
      </form>
    </Panel>
  );
}
