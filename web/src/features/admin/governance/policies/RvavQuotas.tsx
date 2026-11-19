import { useQuery } from "@tanstack/react-query";
import { ErrorState } from "@/components/common/feedback";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { QuotaLines, type QuotaWrite } from "../QuotaLines";

type S = components["schemas"];

/** rvav's quota, as a whole and for each user across it; administrators' only (ADR-0047). */
export function RvavQuotas({ canEdit }: { canEdit: boolean }) {
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
        title="All of rvav"
        description="What every project's deployments and requests not yet done may take together, beside each project's quota."
        label="Limits for all of rvav"
        empty="rvav as a whole may take any amount. Add a limit on vCPU, memory, storage or VMs."
        lines={whole}
        loading={quotas.isPending}
        error={error}
        canEdit={canEdit}
        save={save(false)}
        invalidates={[["admin", "global-quotas"]]}
      />
      <QuotaLines
        title="Each user across rvav"
        description="What each user may hold in all projects together: the deployments they own and their requests not yet done. A request made for someone counts for them."
        label="Limits for each user"
        empty="Each user may take what their projects have left. Add a limit on vCPU, memory, storage or VMs."
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
