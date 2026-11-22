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
  Mail,
  Package,
  Palette,
  Plug,
  Scale,
  ScanSearch,
  ScrollText,
  Send,
  Server,
  Shapes,
  ShieldUser,
  SquareCheckBig,
  Variable,
  Webhook,
  Workflow,
  Wrench,
  type LucideIcon,
} from "lucide-react";
import type { LinkProps } from "@tanstack/react-router";
import type { Me } from "@/lib/api/types";
import { can, canAnywhere } from "@/lib/can";
import { i18n } from "@/lib/i18n";
import type shell from "@/locales/en/shell.json";

export const mainPages = [
  { key: "overview", to: "/", icon: House },
  { key: "catalog", to: "/catalog", icon: LayoutGrid },
  { key: "deployments", to: "/deployments", icon: Server },
  { key: "resources", to: "/resources", icon: HardDrive },
  { key: "requests", to: "/requests", icon: List },
  { key: "approvals", to: "/approvals", icon: SquareCheckBig },
  { key: "reports", to: "/reports", icon: ChartColumn },
] as const;

export type MainPageKey = (typeof mainPages)[number]["key"];
type AdminPageKey = keyof (typeof shell)["nav"]["admin"];
type AdminGroupKey = keyof (typeof shell)["nav"]["groups"];

export interface AdminPage {
  /** Names the menu's label, which is the page's title too (adminLabel). */
  key: AdminPageKey;
  to: NonNullable<LinkProps["to"]>;
  icon: LucideIcon;
  /** More words the command palette finds the page by. */
  keywords?: string[];
  show: (me: Me) => boolean;
}

export interface AdminGroup {
  key: AdminGroupKey;
  pages: AdminPage[];
}

/** A main page's label, its title too; read it while rendering, in a component that calls useTranslation. */
export const mainLabel = (key: MainPageKey) => i18n.t(`shell:nav.main.${key}`);

/** An administration page's label, its title too. */
export const adminLabel = (page: AdminPage) => i18n.t(`shell:nav.admin.${page.key}`);

/** An administration group's heading. */
export const groupLabel = (group: AdminGroup) => i18n.t(`shell:nav.groups.${group.key}`);

// Each page for those who may use it (ADR-0020, ADR-0034); the server
// decides again.
export const adminGroups: AdminGroup[] = [
  {
    key: "catalog",
    pages: [
      { to: "/admin/catalog-items", key: "catalogItems", icon: Package, show: (me) => can(me, "catalog.manage") },
      {
        to: "/admin/property-groups",
        key: "propertyGroups",
        icon: Boxes,
        keywords: ["inputs", "constants", "propgroup"],
        show: (me) => can(me, "catalog.manage"),
      },
      {
        to: "/admin/variables",
        key: "variables",
        icon: Variable,
        keywords: ["configuration elements", "shared values", "vars"],
        show: (me) => can(me, "catalog.manage"),
      },
      {
        to: "/admin/form-library",
        key: "formLibrary",
        icon: Library,
        keywords: ["value sources", "validations", "vro actions", "external sources"],
        show: (me) => can(me, "catalog.manage"),
      },
      {
        to: "/admin/day2-actions",
        key: "day2Actions",
        icon: Wrench,
        show: (me) => can(me, "extensibility.manage"),
      },
      {
        to: "/admin/workflows",
        key: "workflows",
        icon: Workflow,
        show: (me) => can(me, "extensibility.read"),
      },
      {
        to: "/admin/resource-types",
        key: "resourceTypes",
        icon: Shapes,
        keywords: ["custom resources", "load balancers"],
        show: (me) => can(me, "extensibility.read"),
      },
      {
        to: "/admin/subscriptions",
        key: "subscriptions",
        icon: Webhook,
        keywords: ["webhooks", "events"],
        show: (me) => can(me, "extensibility.read"),
      },
    ],
  },
  {
    key: "governance",
    pages: [
      {
        to: "/admin/projects",
        key: "projects",
        icon: Folders,
        keywords: ["environments", "quotas", "placement"],
        show: (me) => canAnywhere(me, "project.settings.read"),
      },
      {
        to: "/admin/roles",
        key: "roles",
        icon: ShieldUser,
        keywords: ["permissions"],
        show: (me) => can(me, "access.read"),
      },
      {
        to: "/admin/policies",
        key: "policies",
        icon: Scale,
        keywords: ["approval", "lease", "naming"],
        show: (me) => can(me, "policies.read") || canAnywhere(me, "project.policies.manage"),
      },
    ],
  },
  {
    key: "infrastructure",
    pages: [
      {
        to: "/admin/integrations",
        key: "integrations",
        icon: Plug,
        keywords: ["vcenter", "awx", "efficientip", "nsx", "git"],
        show: (me) => can(me, "integrations.manage"),
      },
      {
        to: "/admin/infrastructure",
        key: "infrastructure",
        icon: Layers,
        keywords: ["sizes", "subnets", "tags"],
        show: (me) => can(me, "infrastructure.read"),
      },
      {
        to: "/admin/onboarding",
        key: "onboarding",
        icon: ScanSearch,
        keywords: ["adopt"],
        show: (me) => can(me, "import.manage"),
      },
      { to: "/admin/vra-import", key: "vraImport", icon: Import, show: (me) => can(me, "import.manage") },
    ],
  },
  {
    key: "operations",
    pages: [
      {
        to: "/admin/setup",
        key: "setup",
        icon: ListChecks,
        keywords: ["checklist"],
        show: (me) => can(me, "system.manage"),
      },
      {
        to: "/admin/branding",
        key: "branding",
        icon: Palette,
        keywords: ["portal name", "logo", "accent colour", "theme"],
        show: (me) => can(me, "system.manage"),
      },
      {
        to: "/admin/notification-templates",
        key: "notificationTemplates",
        icon: Mail,
        keywords: ["email", "teams", "slack", "messages"],
        show: (me) => can(me, "system.manage"),
      },
      { to: "/admin/audit", key: "audit", icon: ScrollText, show: (me) => canAnywhere(me, "audit.read") },
      {
        to: "/admin/audit-forwarding",
        key: "auditForwarding",
        icon: Send,
        keywords: ["syslog", "siem", "webhook"],
        show: (me) => can(me, "integrations.manage") || can(me, "audit.read"),
      },
      {
        to: "/admin/system",
        key: "system",
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
