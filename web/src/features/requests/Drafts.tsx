// Requests saved to finish later (S11.6): resumed in the request form, or
// discarded.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Time } from "@/components/common/misc";
import { Panel } from "@/components/ui/panel";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { api, call, isApiError } from "@/lib/api/client";
import { q } from "@/lib/query";

export function Drafts() {
  const { t } = useTranslation("requests");
  const qc = useQueryClient();
  const drafts = useQuery(q.drafts());
  const [discarding, setDiscarding] = useState<{ id: string; name: string } | null>(null);
  const discard = useMutation({
    mutationFn: (id: string) => call(api.DELETE("/me/drafts/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["me", "drafts"] });
      toast.success(t("drafts.discarded"));
      setDiscarding(null);
    },
    onError: (e) => toast.error(isApiError(e) ? e.problem.title : t("drafts.notDiscarded")),
  });
  const items = drafts.data?.items ?? [];
  if (items.length === 0) return null;
  return (
    <Panel title={t("drafts.title")} flush>
      <ul aria-label={t("drafts.title")} className="divide-y divide-line">
        {items.map((d) => (
          <li key={d.id} className="flex flex-wrap items-center gap-3 px-4 py-2.5 text-body">
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="font-medium">{d.name}</span>
              <span className="text-secondary text-muted">
                {d.catalog_item.name} · {d.project.name} · {d.draft.environment}
                {d.on_behalf_of ? ` · ${t("drafts.for", { name: d.on_behalf_of.name })}` : ""} ·{" "}
                <Trans t={t} i18nKey="drafts.saved" components={{ time: <Time iso={d.updated_at} /> }} />
              </span>
            </div>
            <Button size="sm" asChild>
              <Link to="/catalog/$itemId/request" params={{ itemId: d.catalog_item.slug }} search={{ draft: d.id }}>
                {t("drafts.resume")}
              </Link>
            </Button>
            <Button
              size="sm"
              variant="quiet"
              aria-label={t("drafts.discardName", { name: d.name })}
              onClick={() => setDiscarding({ id: d.id, name: d.name })}
            >
              {t("drafts.discard")}
            </Button>
          </li>
        ))}
      </ul>
      {discarding && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setDiscarding(null)}
          title={t("drafts.confirmTitle", { name: discarding.name })}
          description={t("drafts.confirmBody")}
          confirmLabel={t("drafts.confirm")}
          pending={discard.isPending}
          onConfirm={() => discard.mutate(discarding.id)}
        />
      )}
    </Panel>
  );
}
