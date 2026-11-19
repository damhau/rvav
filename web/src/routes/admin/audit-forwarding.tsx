import { createFileRoute } from "@tanstack/react-router";
import { AuditForwardingPage } from "@/features/admin/AuditForwardingPage";

export const Route = createFileRoute("/admin/audit-forwarding")({
  component: AuditForwardingPage,
});
