// The server's refusals, said where they happened (ADR-0034).
import { TriangleAlert } from "lucide-react";
import { Trans, useTranslation } from "react-i18next";
import { isApiError } from "@/lib/api/client";

const REFUSED = [403, 409, 422];

/** Says whether an error is a refusal to show in place, rather than in a toast. */
export function refused(error: unknown, only = REFUSED): boolean {
  return isApiError(error) && only.includes(error.status);
}

export function Refusal({ error, only = REFUSED }: { error: unknown; only?: number[] }) {
  const { t } = useTranslation("admin-access");
  if (!isApiError(error) || !refused(error, only)) return null;
  const p = error.problem;
  const text =
    error.status === 403 && p.detail
      ? t("refusal.titleDetail", { title: p.title, detail: p.detail })
      : (p.detail ?? error.fieldErrors[0]?.message ?? p.title);
  return (
    <p role="alert" className="flex items-start gap-1.5 text-secondary text-bad">
      <TriangleAlert className="mt-px size-3.5 shrink-0" aria-hidden />
      {text}
    </p>
  );
}

/** What access.manage makes of whoever holds it. */
export function AdminWarning({ who }: { who?: string }) {
  const { t } = useTranslation("admin-access");
  return (
    <p className="flex items-start gap-1.5 rounded-md bg-warn-soft px-3 py-2 text-secondary text-warn">
      <TriangleAlert className="mt-px size-3.5 shrink-0" aria-hidden />
      <span>
        <Trans
          t={t}
          i18nKey="refusal.adminWarning"
          values={{ who: who ?? t("refusal.whoeverHoldsThis") }}
          components={{ mono: <span className="font-mono" /> }}
        />
      </span>
    </p>
  );
}
