import { createFileRoute } from "@tanstack/react-router";
import { Day2FormPage } from "@/features/admin/Day2FormPage";

export const Route = createFileRoute("/admin/day2-actions_/$key/form")({
  component: function Day2FormRoute() {
    const { key } = Route.useParams();
    return <Day2FormPage actionKey={key} />;
  },
});
