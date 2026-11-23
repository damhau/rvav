import { createFileRoute } from "@tanstack/react-router";
import { AuditPage, type AuditSearch } from "@/features/admin/AuditPage";
import { optDay, optString } from "../-search";

export const Route = createFileRoute("/admin/audit")({
  validateSearch: (s: Record<string, unknown>): AuditSearch => ({
    q: optString(s.q),
    kind: optString(s.kind, 80),
    project: optString(s.project, 80),
    from: optDay(s.from),
    to: optDay(s.to),
  }),
  component: AuditPage,
});
