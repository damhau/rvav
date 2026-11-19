import {
  Activity,
  Boxes,
  ChartColumn,
  Folders,
  HardDrive,
  House,
  Import,
  Layers,
  LayoutGrid,
  Library,
  List,
  ListChecks,
  Package,
  Plug,
  Scale,
  ScanSearch,
  ScrollText,
  Server,
  ShieldUser,
  SquareCheckBig,
  Webhook,
  Workflow,
  Wrench,
  type LucideIcon,
} from "lucide-react";
import type { LinkProps } from "@tanstack/react-router";
import type { Me } from "@/lib/api/types";
import { can, canAnywhere } from "@/lib/can";

export const mainPages = [
  { key: "overview", to: "/", label: "Overview", icon: House },
  { key: "catalog", to: "/catalog", label: "Catalog", icon: LayoutGrid },
  { key: "deployments", to: "/deployments", label: "Deployments", icon: Server },
  { key: "resources", to: "/resources", label: "Resources", icon: HardDrive },
  { key: "requests", to: "/requests", label: "Requests", icon: List },
  { key: "approvals", to: "/approvals", label: "Approvals", icon: SquareCheckBig },
  { key: "reports", to: "/reports", label: "Reports", icon: ChartColumn },
] as const;

export interface AdminPage {
  to: NonNullable<LinkProps["to"]>;
  /** The menu's label: the page's title too. */
  label: string;
  icon: LucideIcon;
  /** More words the command palette finds the page by. */
  keywords?: string[];
  show: (me: Me) => boolean;
}

export interface AdminGroup {
  key: string;
  label: string;
  pages: AdminPage[];
}

// Each page for those who may use it (ADR-0020, ADR-0034); the server
// decides again.
export const adminGroups: AdminGroup[] = [
  {
    key: "catalog",
    label: "Catalog",
    pages: [
      { to: "/admin/catalog-items", label: "Catalog items", icon: Package, show: (me) => can(me, "catalog.manage") },
      {
        to: "/admin/property-groups",
        label: "Property groups",
        icon: Boxes,
        keywords: ["inputs", "constants", "propgroup"],
        show: (me) => can(me, "catalog.manage"),
      },
      {
        to: "/admin/form-library",
        label: "Form library",
        icon: Library,
        keywords: ["value sources", "validations", "vro actions", "external sources"],
        show: (me) => can(me, "catalog.manage"),
      },
      {
        to: "/admin/day2-actions",
        label: "Day-2 actions",
        icon: Wrench,
        show: (me) => can(me, "extensibility.manage"),
      },
      {
        to: "/admin/workflows",
        label: "Custom workflows",
        icon: Workflow,
        show: (me) => can(me, "extensibility.read"),
      },
      {
        to: "/admin/subscriptions",
        label: "Subscriptions",
        icon: Webhook,
        keywords: ["webhooks", "events"],
        show: (me) => can(me, "extensibility.read"),
      },
    ],
  },
  {
    key: "governance",
    label: "Governance",
    pages: [
      {
        to: "/admin/projects",
        label: "Projects",
        icon: Folders,
        keywords: ["environments", "quotas", "placement"],
        show: (me) => canAnywhere(me, "project.settings.read"),
      },
      {
        to: "/admin/roles",
        label: "Roles",
        icon: ShieldUser,
        keywords: ["permissions"],
        show: (me) => can(me, "access.read"),
      },
      {
        to: "/admin/policies",
        label: "Policies",
        icon: Scale,
        keywords: ["approval", "lease", "naming"],
        show: (me) => can(me, "policies.read") || canAnywhere(me, "project.policies.manage"),
      },
    ],
  },
  {
    key: "infrastructure",
    label: "Infrastructure",
    pages: [
      {
        to: "/admin/integrations",
        label: "Integrations",
        icon: Plug,
        keywords: ["vcenter", "awx", "efficientip", "nsx", "git"],
        show: (me) => can(me, "integrations.manage"),
      },
      {
        to: "/admin/infrastructure",
        label: "Templates and networks",
        icon: Layers,
        keywords: ["sizes", "subnets", "tags"],
        show: (me) => can(me, "infrastructure.read"),
      },
      {
        to: "/admin/onboarding",
        label: "Onboarding",
        icon: ScanSearch,
        keywords: ["adopt"],
        show: (me) => can(me, "import.manage"),
      },
      { to: "/admin/vra-import", label: "vRA import", icon: Import, show: (me) => can(me, "import.manage") },
    ],
  },
  {
    key: "operations",
    label: "Operations",
    pages: [
      {
        to: "/admin/setup",
        label: "Setup",
        icon: ListChecks,
        keywords: ["checklist"],
        show: (me) => can(me, "system.manage"),
      },
      { to: "/admin/audit", label: "Audit log", icon: ScrollText, show: (me) => canAnywhere(me, "audit.read") },
      {
        to: "/admin/system",
        label: "System",
        icon: Activity,
        keywords: ["version", "health"],
        show: (me) => can(me, "system.manage"),
      },
    ],
  },
];

/** The administration page an address is on: the page itself or one below it, such as an item of Catalog items. */
export function adminPageAt(path: string): AdminPage | undefined {
  return adminGroups.flatMap((g) => g.pages).find((p) => path === p.to || path.startsWith(`${p.to}/`));
}

/** The administration pages someone may open, by group; a group with none is left out. */
export function adminMenu(me: Me): AdminGroup[] {
  return adminGroups.map((g) => ({ ...g, pages: g.pages.filter((p) => p.show(me)) })).filter((g) => g.pages.length > 0);
}
