import { createFileRoute, Outlet, useRouterState } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { PageNotAllowed } from "@/components/common/blocked";
import { adminLabel, adminPageAt } from "@/components/shell/nav";
import { useCurrentProject } from "@/lib/project";

// Each administration page opens for those its menu entry shows to (nav.ts);
// the server decides again on everything the page reads and changes.
export const Route = createFileRoute("/admin")({
  component: function AdminGuard() {
    useTranslation();
    const { me } = useCurrentProject();
    const path = useRouterState({ select: (s) => s.location.pathname });
    const page = adminPageAt(path);
    return page && !page.show(me) ? <PageNotAllowed page={adminLabel(page)} /> : <Outlet />;
  },
});
