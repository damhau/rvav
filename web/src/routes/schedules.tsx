import { createFileRoute } from "@tanstack/react-router";
import { SchedulesPage } from "@/features/requests/SchedulesPage";

export const Route = createFileRoute("/schedules")({
  component: SchedulesPage,
});
