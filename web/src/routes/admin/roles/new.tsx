import { createFileRoute } from "@tanstack/react-router";
import { NewRolePage } from "@/features/admin/access/RolePage";
import { optString } from "../../-search";

export const Route = createFileRoute("/admin/roles/new")({
  // from: the role whose permissions the new one starts with.
  validateSearch: (s: Record<string, unknown>): { from?: string } => ({ from: optString(s.from, 41) }),
  component: function NewRoleRoute() {
    const { from } = Route.useSearch();
    return <NewRolePage key={from ?? ""} from={from} />;
  },
});
