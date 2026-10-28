import { createFileRoute } from "@tanstack/react-router";
import { NotInSlice } from "@/features/NotInSlice";

const titles: Record<string, string> = {
  "catalog-items": "Catalog items",
  "day2-actions": "Day-2 actions",
  integrations: "Integrations",
  audit: "Audit log",
};

export const Route = createFileRoute("/admin/$section")({
  component: function AdminRoute() {
    const { section } = Route.useParams();
    return (
      <NotInSlice
        title={titles[section] ?? "Administration"}
        what="Administration screens (catalog items and forms, policies, projects, integrations, audit) come after the self-service slices."
      />
    );
  },
});
