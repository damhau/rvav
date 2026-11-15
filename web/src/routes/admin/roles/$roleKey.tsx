import { createFileRoute } from "@tanstack/react-router";
import { RolePage } from "@/features/admin/access/RolePage";

export const Route = createFileRoute("/admin/roles/$roleKey")({
  component: function RoleRoute() {
    const { roleKey } = Route.useParams();
    return <RolePage key={roleKey} roleKey={roleKey} />;
  },
});
