import { createFileRoute } from "@tanstack/react-router";
import { SetupPage } from "@/features/admin/SetupPage";

export const Route = createFileRoute("/admin/setup")({
  component: SetupPage,
});
