import { createFileRoute } from "@tanstack/react-router";
import { AuditPage } from "@/features/admin/AuditPage";

export const Route = createFileRoute("/admin/audit")({
  component: AuditPage,
});
