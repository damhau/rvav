import { createFileRoute } from "@tanstack/react-router";
import { ReportsPage, reportIntervals, reportPeriods, type ReportsSearch } from "@/features/reports/ReportsPage";
import { optEnum, optString } from "./-search";

export const Route = createFileRoute("/reports")({
  validateSearch: (s: Record<string, unknown>): ReportsSearch => ({
    period: optEnum(s.period, reportPeriods),
    by: optEnum(s.by, reportIntervals),
    project: optString(s.project),
    env: optString(s.env, 40),
  }),
  component: function ReportsRoute() {
    return <ReportsPage search={Route.useSearch()} />;
  },
});
