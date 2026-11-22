import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ErrorState } from "@/components/common/feedback";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { QuotaLines, type QuotaWrite } from "../QuotaLines";

type S = components["schemas"];

/** rvav's quota, as a whole and for each user across it; administrators' only (ADR-0047). */
export function RvavQuotas({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation("admin-governance");
  const quotas = useQuery(adm.globalQuotas());
  const items = quotas.data?.items;
  const whole = items?.filter((q) => !q.per_user);
  const each = items?.filter((q) => q.per_user);
  const error = quotas.isError && <ErrorState error={quotas.error} onRetry={() => void quotas.refetch()} />;
  const save = (perUser: boolean) => (lines: QuotaWrite[]) => {
    const others: S["GlobalQuota"][] = (perUser ? whole : each) ?? [];
    return call(
      api.PUT("/admin/quotas", {
        body: {
          items: [
            ...others.map(({ resource, limit, enforcement, per_user }) => ({ resource, limit, enforcement, per_user })),
            ...lines.map((l) => ({ ...l, per_user: perUser })),
          ],
        },
      }),
    );
  };
  return (
    <div className="flex flex-col gap-4">
      <QuotaLines
        title={t("policies.quotas.allTitle")}
        description={t("policies.quotas.allLead")}
        label={t("policies.quotas.allLabel")}
        empty={t("policies.quotas.allEmpty")}
        lines={whole}
        loading={quotas.isPending}
        error={error}
        canEdit={canEdit}
        save={save(false)}
        invalidates={[["admin", "global-quotas"]]}
      />
      <QuotaLines
        title={t("policies.quotas.eachTitle")}
        description={t("policies.quotas.eachLead")}
        label={t("policies.quotas.eachLabel")}
        empty={t("policies.quotas.eachEmpty")}
        lines={each}
        loading={quotas.isPending}
        error={error}
        canEdit={canEdit}
        save={save(true)}
        invalidates={[["admin", "global-quotas"]]}
      />
    </div>
  );
}
