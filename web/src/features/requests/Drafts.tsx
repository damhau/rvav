// Requests saved to finish later (S11.6): resumed in the request form, or
// discarded.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { toast } from "sonner";
import { Time } from "@/components/common/misc";
import { Panel } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { api, call } from "@/lib/api/client";
import { q } from "@/lib/query";

export function Drafts() {
  const qc = useQueryClient();
  const drafts = useQuery(q.drafts());
  const discard = useMutation({
    mutationFn: (id: string) => call(api.DELETE("/me/drafts/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["me", "drafts"] });
      toast.success("Draft discarded");
    },
  });
  const items = drafts.data?.items ?? [];
  if (items.length === 0) return null;
  return (
    <Panel title="Your drafts" flush>
      <ul aria-label="Your drafts" className="divide-y divide-line">
        {items.map((d) => (
          <li key={d.id} className="flex flex-wrap items-center gap-3 px-4 py-2.5 text-[13px]">
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="font-medium">{d.name}</span>
              <span className="text-xs text-muted">
                {d.catalog_item.name} · {d.project.name} · {d.draft.environment}
                {d.on_behalf_of ? ` · for ${d.on_behalf_of.name}` : ""} · saved <Time iso={d.updated_at} />
              </span>
            </div>
            <Button size="sm" variant="primary" asChild>
              <Link to="/catalog/$itemId/request" params={{ itemId: d.catalog_item.slug }} search={{ draft: d.id }}>
                Resume
              </Link>
            </Button>
            <Button
              size="sm"
              variant="ghost"
              aria-label={`Discard ${d.name}`}
              disabled={discard.isPending}
              onClick={() => discard.mutate(d.id)}
            >
              Discard
            </Button>
          </li>
        ))}
      </ul>
    </Panel>
  );
}
