import { createFileRoute } from "@tanstack/react-router";
import { Day2ActionsPage } from "@/features/admin/Day2ActionsPage";

export const Route = createFileRoute("/admin/day2-actions")({
  component: Day2ActionsPage,
});
