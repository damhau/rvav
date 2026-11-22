import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ErrorState } from "@/components/common/feedback";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { QuotaLines } from "./QuotaLines";

type S = components["schemas"];

/**
 * Each member's quota in a project (ADR-0047): whoever manages its quotas
 * writes any limit; its admins write within the project's own, and leave
 * the lines an administrator set alone.
 */
export function MemberQuotas({
  project,
  isAdmin,
  isProjectAdmin,
}: {
  project: S["AdminProject"];
  isAdmin: boolean;
  isProjectAdmin: boolean;
}) {
  const { t } = useTranslation("admin-governance");
  const lines = useQuery(adm.userQuotas(project.id));
  const own = useQuery({ ...adm.quotas(project.id), enabled: !isAdmin && isProjectAdmin });
  const cap = (r: S["QuotaResource"]) => {
    const l = own.data?.items.find((x) => x.resource === r && !x.environment && x.limit != null);
    return l ? t("quota.member.cap", { limit: l.limit, project: project.name }) : undefined;
  };
  return (
    <QuotaLines
      title={t("quota.member.title")}
      description={t("quota.member.lead")}
      label={t("quota.member.label")}
      empty={t("quota.member.empty")}
      lines={lines.data?.items}
      loading={lines.isPending}
      error={lines.isError && <ErrorState error={lines.error} onRetry={() => void lines.refetch()} />}
      canEdit={isAdmin || isProjectAdmin}
      locked={(l) => !isAdmin && Boolean(l.set_by_admin)}
      cap={isAdmin ? undefined : cap}
      save={(items) =>
        call(
          api.PUT("/admin/projects/{projectId}/user-quotas", {
            params: { path: { projectId: project.id } },
            body: { items },
          }),
        )
      }
      invalidates={[["admin", "user-quotas", project.id]]}
    />
  );
}
