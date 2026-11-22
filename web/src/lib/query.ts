import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api, call } from "./api/client";
import type { components, operations } from "./api/schema";
import type {
  ApprovalStatus,
  AuditEventPage,
  CatalogItemType,
  DeploymentStatusFilter,
  EnvironmentKey,
  NotificationKind,
  RequestStatus,
  TemplateChannel,
  TemplateLocale,
  ViewList,
  VmPowerState,
} from "./api/types";

export interface CatalogFilters {
  projectId: string;
  category?: string;
  q?: string;
  type?: CatalogItemType;
  favourite?: boolean;
}

export interface RequestFilters {
  projectId: string;
  status?: RequestStatus[];
  requester?: string;
  q?: string;
  limit?: number;
}

type DeploymentQuery = NonNullable<operations["listDeployments"]["parameters"]["query"]>;
type VmQuery = NonNullable<operations["listVms"]["parameters"]["query"]>;
export type DeploymentSort = NonNullable<DeploymentQuery["sort"]>;
export type VmSort = NonNullable<VmQuery["sort"]>;

export interface DeploymentFilters {
  /** Empty for every project the caller sees. */
  projectId: string;
  status?: DeploymentStatusFilter[];
  environment?: EnvironmentKey[];
  /** Catalog items, by slug. */
  item?: string[];
  /** VM templates, by name. */
  template?: string[];
  owner?: string;
  q?: string;
  leaseExpiresBefore?: string;
  sort?: DeploymentSort;
  limit?: number;
}

export interface VmFilters {
  /** Empty for every project the caller sees. */
  projectId: string;
  environment?: EnvironmentKey[];
  powerState?: VmPowerState[];
  vcenter?: string;
  cluster?: string;
  network?: string;
  q?: string;
  sort?: VmSort;
  limit?: number;
}

export interface AdminItemFilters {
  q?: string;
  category?: string;
  source?: "native" | "awx";
  state?: "published" | "unpublished";
}

export interface AuditFilters {
  q?: string;
  action?: string;
  project_id?: string;
  from?: string;
  to?: string;
}

// Query key factories: one place per resource, so invalidation stays precise.
export const keys = {
  me: ["me"] as const,
  ipamReconcile: ["admin", "ipam", "reconcile"] as const,
  awxTemplates: ["admin", "awx", "templates"] as const,
  awxActions: ["admin", "awx", "actions"] as const,
  day2Forms: ["admin", "day2-forms"] as const,
  day2Form: (action: string) => ["admin", "day2-forms", action] as const,
  catalogAdmin: {
    all: ["admin", "catalog"] as const,
    items: (f: AdminItemFilters) => ["admin", "catalog", "items", f] as const,
    item: (id: string) => ["admin", "catalog", "item", id] as const,
    version: (itemId: string, versionId: string) => ["admin", "catalog", "version", itemId, versionId] as const,
    compare: (itemId: string, from: string, to: string) => ["admin", "catalog", "compare", itemId, from, to] as const,
    categories: ["admin", "catalog", "categories"] as const,
    icons: ["admin", "catalog", "icons"] as const,
    compositeSteps: ["admin", "catalog", "composite-steps"] as const,
    git: ["admin", "catalog", "git"] as const,
    itemGit: (id: string) => ["admin", "catalog", "item-git", id] as const,
    formLibrary: ["admin", "catalog", "form-library"] as const,
    formLibraryEntry: (key: string, version?: number) => ["admin", "catalog", "form-library", key, version] as const,
  },
  tokens: (all: boolean) => ["tokens", all] as const,
  audit: (f: AuditFilters) => ["audit", f] as const,
  environments: ["environments"] as const,
  project: (id: string) => ["projects", id] as const,
  quota: (id: string, environment?: string) => ["projects", id, "quota", environment ?? "all"] as const,
  catalog: {
    all: ["catalog"] as const,
    categories: (projectId: string) => ["catalog", "categories", projectId] as const,
    lists: () => ["catalog", "list"] as const,
    list: (f: CatalogFilters) => ["catalog", "list", f] as const,
    item: (key: string, projectId: string) => ["catalog", "item", key, projectId] as const,
    form: (key: string, projectId: string, versionId?: string) =>
      ["catalog", "form", key, projectId, versionId ?? "default"] as const,
  },
  fieldValues: (versionId: string, projectId: string, environment: string, field: string, deps: unknown[]) =>
    ["forms", "values", versionId, projectId, environment, field, deps] as const,
  preview: (draft: unknown) => ["requests", "preview", draft] as const,
  requests: {
    all: ["requests"] as const,
    lists: () => ["requests", "list"] as const,
    list: (f: RequestFilters) => ["requests", "list", f] as const,
    detail: (key: string) => ["requests", "detail", key] as const,
    steps: (requestId: string) => ["requests", "steps", requestId] as const,
    topology: (requestId: string) => ["requests", "topology", requestId] as const,
    logHead: (requestId: string, stepId: string) => ["requests", "log-head", requestId, stepId] as const,
    /** A step's data, read again whenever the step changes. */
    stepData: (requestId: string, stepId: string, at: string) =>
      ["requests", "step-data", requestId, stepId, at] as const,
  },
  approvals: {
    all: ["approvals"] as const,
    lists: () => ["approvals", "list"] as const,
    list: (status: ApprovalStatus[]) => ["approvals", "list", status] as const,
    detail: (id: string) => ["approvals", "detail", id] as const,
    forRequest: (requestId: string) => ["approvals", "request", requestId] as const,
  },
  deployments: {
    all: ["deployments"] as const,
    lists: () => ["deployments", "list"] as const,
    list: (f: DeploymentFilters) => ["deployments", "list", f] as const,
    detail: (id: string) => ["deployments", "detail", id] as const,
    actions: (id: string) => ["deployments", "actions", id] as const,
    updatePreview: (id: string, draft: unknown) => ["deployments", "update-preview", id, draft] as const,
    actionPreview: (id: string, draft: unknown) => ["deployments", "action-preview", id, draft] as const,
    history: (id: string) => ["deployments", "history", id] as const,
    facets: (projectId: string) => ["deployments", "facets", projectId] as const,
  },
  vms: {
    all: ["vms"] as const,
    list: (f: VmFilters) => ["vms", "list", f] as const,
    facets: (projectId: string) => ["vms", "facets", projectId] as const,
  },
  views: (list: ViewList) => ["me", "views", list] as const,
};

const csv = <T extends string>(v?: T[]) => (v && v.length ? v : undefined);

const get = <T>(key: readonly unknown[], fn: (signal: AbortSignal) => Promise<T>) =>
  queryOptions({ queryKey: key, queryFn: ({ signal }) => fn(signal) });

/** Administration (S9, ADR-0020). */
export const adm = {
  projects: (search: string, archived: boolean) =>
    get(["admin", "projects", search, archived], (signal) =>
      call(api.GET("/admin/projects", { params: { query: { q: search || undefined, archived } }, signal })),
    ),
  /** Every project's ID, code and name, for the pages that pick projects without reading their settings. */
  projectNames: (archived = false) =>
    get(["admin", "project-names", archived], (signal) =>
      call(api.GET("/admin/project-names", { params: { query: { archived } }, signal })),
    ),
  project: (id: string) =>
    get(["admin", "project", id], (signal) =>
      call(api.GET("/admin/projects/{projectId}", { params: { path: { projectId: id } }, signal })),
    ),
  members: (id: string) =>
    get(["admin", "members", id], (signal) =>
      call(api.GET("/projects/{projectId}/members", { params: { path: { projectId: id } }, signal })),
    ),
  groupMappings: () => get(["admin", "group-mappings"], (signal) => call(api.GET("/admin/group-mappings", { signal }))),
  roles: () => get(["admin", "roles"], (signal) => call(api.GET("/admin/roles", { signal }))),
  role: (key: string) =>
    get(["admin", "roles", key], (signal) =>
      call(api.GET("/admin/roles/{key}", { params: { path: { key } }, signal })),
    ),
  permissions: () => get(["permissions"], (signal) => call(api.GET("/permissions", { signal }))),
  roleAssignments: () =>
    get(["admin", "role-assignments"], (signal) => call(api.GET("/admin/role-assignments", { signal }))),
  assignableRoles: (projectId: string) =>
    get(["admin", "assignable-roles", projectId], (signal) =>
      call(api.GET("/projects/{projectId}/assignable-roles", { params: { path: { projectId } }, signal })),
    ),
  explain: (userId: string, projectId: string | undefined) =>
    get(["admin", "explain", userId, projectId ?? ""], (signal) =>
      call(api.GET("/admin/access/explain", { params: { query: { user_id: userId, project_id: projectId } }, signal })),
    ),
  environments: () => get(["admin", "environments"], (signal) => call(api.GET("/admin/environments", { signal }))),
  placementRules: (projectId: string) =>
    get(["admin", "placement-rules", projectId], (signal) =>
      call(api.GET("/admin/placement-rules", { params: { query: { project_id: projectId } }, signal })),
    ),
  /** A project's Active Directory OUs and those of every project, or every one (ADR-0064). */
  adPlacements: (projectId?: string) =>
    get(["admin", "ad-placements", projectId ?? ""], (signal) =>
      call(api.GET("/admin/ad-placements", { params: { query: projectId ? { project_id: projectId } : {} }, signal })),
    ),
  placementPreview: (projectId: string, environment: string) =>
    get(["admin", "placement-preview", projectId, environment], (signal) =>
      call(api.GET("/admin/placement-preview", { params: { query: { project_id: projectId, environment } }, signal })),
    ),
  inventory: () => get(["admin", "inventory"], (signal) => call(api.GET("/admin/inventory", { signal }))),
  networkMappings: () =>
    get(["admin", "network-mappings"], (signal) => call(api.GET("/admin/network-mappings", { signal }))),
  templates: () => get(["admin", "templates"], (signal) => call(api.GET("/admin/templates", { signal }))),
  sizes: () => get(["admin", "sizes"], (signal) => call(api.GET("/admin/sizes", { signal }))),
  naming: () => get(["admin", "naming"], (signal) => call(api.GET("/admin/naming-policies", { signal }))),
  quotas: (projectId: string) =>
    get(["admin", "quotas", projectId], (signal) =>
      call(api.GET("/admin/projects/{projectId}/quotas", { params: { path: { projectId } }, signal })),
    ),
  userQuotas: (projectId: string) =>
    get(["admin", "user-quotas", projectId], (signal) =>
      call(api.GET("/admin/projects/{projectId}/user-quotas", { params: { path: { projectId } }, signal })),
    ),
  globalQuotas: () => get(["admin", "global-quotas"], (signal) => call(api.GET("/admin/quotas", { signal }))),
  leases: () => get(["admin", "leases"], (signal) => call(api.GET("/admin/lease-policies", { signal }))),
  leaseRules: () => get(["admin", "lease-rules"], (signal) => call(api.GET("/admin/lease-rules", { signal }))),
  limitPolicies: () =>
    get(["admin", "deployment-limit-policies"], (signal) =>
      call(api.GET("/admin/deployment-limit-policies", { signal })),
    ),
  approvals: () =>
    get(["admin", "approval-policies"], (signal) => call(api.GET("/admin/approval-policies", { signal }))),
  day2: () => get(["admin", "day2-policies"], (signal) => call(api.GET("/admin/day2-policies", { signal }))),
  integrations: () => get(["admin", "integrations"], (signal) => call(api.GET("/admin/integrations", { signal }))),
  processes: () => get(["admin", "processes"], (signal) => call(api.GET("/admin/processes", { signal }))),
  setup: () => get(["admin", "setup"], (signal) => call(api.GET("/admin/setup", { signal }))),
  system: () => get(["admin", "system"], (signal) => call(api.GET("/admin/system", { signal }))),
  inventoryToken: () =>
    get(["admin", "inventory-token"], (signal) => call(api.GET("/admin/integrations/awx/inventory-token", { signal }))),
  vcenters: () => get(["admin", "vcenters"], (signal) => call(api.GET("/admin/vcenters", { signal }))),
  nsxManagers: () => get(["admin", "nsx-managers"], (signal) => call(api.GET("/admin/nsx-managers", { signal }))),
  auditDestinations: () =>
    get(["admin", "audit-destinations"], (signal) => call(api.GET("/admin/audit-destinations", { signal }))),
  // Events, subscriptions and custom workflows (S10, ADR-0021 and ADR-0022).
  eventTopics: () =>
    queryOptions({
      queryKey: ["admin", "event-topics"],
      queryFn: ({ signal }) => call(api.GET("/admin/event-topics", { signal })),
      staleTime: Infinity,
    }),
  subscriptions: () => get(["admin", "subscriptions"], (signal) => call(api.GET("/admin/subscriptions", { signal }))),
  subscription: (id: string) =>
    get(["admin", "subscription", id], (signal) =>
      call(api.GET("/admin/subscriptions/{id}", { params: { path: { id } }, signal })),
    ),
  subscriptionRuns: (id: string, status: string) =>
    infiniteQueryOptions({
      queryKey: ["admin", "subscription-runs", id, status],
      queryFn: async ({ signal, pageParam }) =>
        call(
          api.GET("/admin/subscriptions/{id}/runs", {
            params: {
              path: { id },
              query: {
                status: status === "any" ? undefined : (status as components["schemas"]["SubscriptionRunStatus"]),
                cursor: pageParam,
                limit: 50,
              },
            },
            signal,
          }),
        ),
      initialPageParam: undefined as string | undefined,
      getNextPageParam: (last) => last.next_cursor ?? undefined,
    }),
  subscriptionRun: (id: string) =>
    get(["admin", "subscription-run", id], (signal) =>
      call(api.GET("/admin/subscription-runs/{id}", { params: { path: { id } }, signal })),
    ),
  onboardingPlans: () =>
    get(["admin", "onboarding", "plans"], (signal) => call(api.GET("/admin/onboarding/plans", { signal }))),
  onboardingPlan: (planId: string) =>
    get(["admin", "onboarding", "plan", planId], (signal) =>
      call(api.GET("/admin/onboarding/plans/{planId}", { params: { path: { planId } }, signal })),
    ),
  onboardingRuns: (planId: string) =>
    get(["admin", "onboarding", "runs", planId], (signal) =>
      call(api.GET("/admin/onboarding/plans/{planId}/runs", { params: { path: { planId } }, signal })),
    ),
  onboardingRun: (runId: string) =>
    get(["admin", "onboarding", "run", runId], (signal) =>
      call(api.GET("/admin/onboarding/runs/{runId}", { params: { path: { runId } }, signal })),
    ),
  workflows: () => get(["admin", "workflows"], (signal) => call(api.GET("/admin/workflows", { signal }))),
  resourceTypes: () => get(["admin", "resource-types"], (signal) => call(api.GET("/admin/resource-types", { signal }))),
  variables: () => get(["admin", "variables"], (signal) => call(api.GET("/admin/variables", { signal }))),
  secrets: () => get(["admin", "secrets"], (signal) => call(api.GET("/admin/secrets", { signal }))),
  branding: () => get(["branding"], (signal) => call(api.GET("/branding", { signal }))),
  notificationTemplates: () =>
    get(["admin", "notification-templates"], (signal) => call(api.GET("/admin/notification-templates", { signal }))),
  notificationTemplate: (kind: NotificationKind, channel: TemplateChannel, locale: TemplateLocale) =>
    get(["admin", "notification-templates", kind, channel, locale], (signal) =>
      call(
        api.GET("/admin/notification-templates/{kind}", {
          params: { path: { kind }, query: { channel, locale } },
          signal,
        }),
      ),
    ),
  users: (search: string) =>
    get(["users", search], (signal) =>
      call(api.GET("/users", { params: { query: { q: search, limit: 20 } }, signal })),
    ),
};

export const q = {
  // The caller's notifications and their preferences (S11, ADR-0023).
  notifications: (unread: boolean) =>
    infiniteQueryOptions({
      queryKey: ["me", "notifications", "list", unread],
      queryFn: async ({ signal, pageParam }) =>
        call(
          api.GET("/me/notifications", {
            params: { query: { unread: unread || undefined, cursor: pageParam, limit: 30 } },
            signal,
          }),
        ),
      initialPageParam: undefined as string | undefined,
      getNextPageParam: (last) => last.next_cursor ?? undefined,
    }),
  latestNotifications: () =>
    queryOptions({
      queryKey: ["me", "notifications", "latest"],
      queryFn: ({ signal }) => call(api.GET("/me/notifications", { params: { query: { limit: 8 } }, signal })),
    }),
  drafts: () =>
    queryOptions({
      queryKey: ["me", "drafts"],
      queryFn: ({ signal }) => call(api.GET("/me/drafts", { signal })),
    }),
  schedules: (projectId: string) =>
    queryOptions({
      queryKey: ["schedules", projectId],
      queryFn: ({ signal }) => call(api.GET("/schedules", { params: { query: { project_id: projectId } }, signal })),
    }),
  schedule: (id: string) =>
    queryOptions({
      queryKey: ["schedules", "one", id],
      queryFn: ({ signal }) =>
        call(api.GET("/schedules/{scheduleId}", { params: { path: { scheduleId: id } }, signal })),
    }),
  draft: (id: string) =>
    queryOptions({
      queryKey: ["me", "drafts", id],
      queryFn: ({ signal }) => call(api.GET("/me/drafts/{id}", { params: { path: { id } }, signal })),
    }),
  notificationPreferences: () =>
    queryOptions({
      queryKey: ["me", "notification-preferences"],
      queryFn: ({ signal }) => call(api.GET("/me/notification-preferences", { signal })),
    }),
  tokens: (all: boolean) =>
    queryOptions({
      queryKey: keys.tokens(all),
      queryFn: ({ signal }) => call(api.GET("/tokens", { params: { query: { all } }, signal })),
    }),
  audit: (f: AuditFilters) =>
    infiniteQueryOptions({
      queryKey: keys.audit(f),
      // JSON here; the same path answers CSV with format=csv.
      queryFn: async ({ signal, pageParam }) =>
        (await call(
          api.GET("/audit", { params: { query: { ...f, limit: 50, cursor: pageParam } }, signal }),
        )) as AuditEventPage,
      initialPageParam: undefined as string | undefined,
      getNextPageParam: (last) => last.next_cursor ?? undefined,
    }),
  adminCatalogItems: (f: AdminItemFilters) =>
    queryOptions({
      queryKey: keys.catalogAdmin.items(f),
      queryFn: ({ signal }) =>
        call(api.GET("/admin/catalog/items", { params: { query: { ...f, limit: 200 } }, signal })),
    }),
  adminCatalogItem: (id: string) =>
    queryOptions({
      queryKey: keys.catalogAdmin.item(id),
      queryFn: ({ signal }) =>
        call(api.GET("/admin/catalog/items/{itemId}", { params: { path: { itemId: id } }, signal })),
    }),
  catalogVersion: (itemId: string, versionId: string) =>
    queryOptions({
      queryKey: keys.catalogAdmin.version(itemId, versionId),
      queryFn: ({ signal }) =>
        call(
          api.GET("/admin/catalog/items/{itemId}/versions/{versionId}", {
            params: { path: { itemId, versionId } },
            signal,
          }),
        ),
    }),
  catalogCompare: (itemId: string, from: string, to: string) =>
    queryOptions({
      queryKey: keys.catalogAdmin.compare(itemId, from, to),
      queryFn: ({ signal }) =>
        call(
          api.GET("/admin/catalog/items/{itemId}/compare", {
            params: { path: { itemId }, query: { from, to } },
            signal,
          }),
        ),
    }),
  catalogGit: () =>
    queryOptions({
      queryKey: keys.catalogAdmin.git,
      queryFn: ({ signal }) => call(api.GET("/admin/catalog/git", { signal })),
    }),
  catalogItemGit: (id: string) =>
    queryOptions({
      queryKey: keys.catalogAdmin.itemGit(id),
      queryFn: ({ signal }) =>
        call(api.GET("/admin/catalog/items/{itemId}/git", { params: { path: { itemId: id } }, signal })),
    }),
  adminCatalogCategories: () =>
    queryOptions({
      queryKey: keys.catalogAdmin.categories,
      queryFn: ({ signal }) => call(api.GET("/admin/catalog/categories", { signal })),
    }),
  formLibrary: () =>
    queryOptions({
      queryKey: keys.catalogAdmin.formLibrary,
      queryFn: ({ signal }) => call(api.GET("/admin/form-library", { signal })),
    }),
  formLibraryEntry: (key: string, version?: number) =>
    queryOptions({
      queryKey: keys.catalogAdmin.formLibraryEntry(key, version),
      queryFn: ({ signal }) =>
        call(api.GET("/admin/form-library/{key}", { params: { path: { key }, query: { version } }, signal })),
      staleTime: version ? Infinity : undefined,
    }),
  compositeStepTargets: () =>
    queryOptions({
      queryKey: keys.catalogAdmin.compositeSteps,
      queryFn: ({ signal }) => call(api.GET("/admin/catalog/composite-steps", { signal })),
    }),
  catalogIcons: () =>
    queryOptions({
      queryKey: keys.catalogAdmin.icons,
      queryFn: ({ signal }) => call(api.GET("/admin/catalog/icons", { signal })),
      staleTime: Infinity,
    }),
  awxActions: () =>
    queryOptions({
      queryKey: keys.awxActions,
      queryFn: ({ signal }) => call(api.GET("/admin/awx/actions", { signal })),
    }),
  awxTemplates: () =>
    queryOptions({
      queryKey: keys.awxTemplates,
      queryFn: ({ signal }) => call(api.GET("/admin/awx/templates", { signal })),
    }),
  ipamReconcile: () =>
    queryOptions({
      queryKey: keys.ipamReconcile,
      queryFn: ({ signal }) => call(api.GET("/admin/ipam/reconcile", { signal })),
      staleTime: Infinity,
    }),
  me: () =>
    queryOptions({
      queryKey: keys.me,
      queryFn: ({ signal }) => call(api.GET("/me", { signal })),
      staleTime: 5 * 60_000,
    }),
  environments: () =>
    queryOptions({
      queryKey: keys.environments,
      queryFn: ({ signal }) => call(api.GET("/environments", { signal })),
      staleTime: 30 * 60_000,
    }),
  project: (id: string) =>
    queryOptions({
      queryKey: keys.project(id),
      queryFn: ({ signal }) => call(api.GET("/projects/{projectId}", { params: { path: { projectId: id } }, signal })),
      staleTime: 5 * 60_000,
    }),
  quota: (id: string, environment?: string) =>
    queryOptions({
      queryKey: keys.quota(id, environment),
      queryFn: ({ signal }) =>
        call(
          api.GET("/projects/{projectId}/quota", {
            params: { path: { projectId: id }, query: { environment } },
            signal,
          }),
        ),
    }),
  categories: (projectId: string) =>
    queryOptions({
      queryKey: keys.catalog.categories(projectId),
      queryFn: ({ signal }) =>
        call(api.GET("/catalog/categories", { params: { query: { project_id: projectId } }, signal })),
      staleTime: 5 * 60_000,
    }),
  catalog: (f: CatalogFilters) =>
    queryOptions({
      queryKey: keys.catalog.list(f),
      queryFn: ({ signal }) =>
        call(
          api.GET("/catalog/items", {
            params: {
              query: {
                project_id: f.projectId,
                category: f.category,
                q: f.q,
                type: f.type,
                favourite: f.favourite,
                limit: 200,
              },
            },
            signal,
          }),
        ),
    }),
  catalogItem: (key: string, projectId: string) =>
    queryOptions({
      queryKey: keys.catalog.item(key, projectId),
      queryFn: ({ signal }) =>
        call(
          api.GET("/catalog/items/{itemId}", {
            params: { path: { itemId: key }, query: { project_id: projectId } },
            signal,
          }),
        ),
    }),
  /** The request form of the item's default version, or of the requestable version named. */
  form: (key: string, projectId: string, versionId?: string) =>
    queryOptions({
      queryKey: keys.catalog.form(key, projectId, versionId),
      queryFn: ({ signal }) =>
        call(
          api.GET("/catalog/items/{itemId}/form", {
            params: {
              path: { itemId: key },
              query: { project_id: projectId, ...(versionId ? { version_id: versionId } : {}) },
            },
            signal,
          }),
        ),
      staleTime: 5 * 60_000,
    }),
  requests: (f: RequestFilters) =>
    infiniteQueryOptions({
      queryKey: keys.requests.list(f),
      initialPageParam: undefined as string | undefined,
      queryFn: ({ pageParam, signal }) =>
        call(
          api.GET("/requests", {
            params: {
              query: {
                project_id: f.projectId,
                status: csv(f.status),
                requester: f.requester,
                q: f.q,
                limit: f.limit ?? 50,
                cursor: pageParam,
              },
            },
            signal,
          }),
        ),
      getNextPageParam: (last) => last.next_cursor ?? undefined,
    }),
  request: (key: string) =>
    queryOptions({
      queryKey: keys.requests.detail(key),
      queryFn: ({ signal }) => call(api.GET("/requests/{requestId}", { params: { path: { requestId: key } }, signal })),
    }),
  steps: (requestId: string) =>
    queryOptions({
      queryKey: keys.requests.steps(requestId),
      queryFn: ({ signal }) =>
        call(api.GET("/requests/{requestId}/steps", { params: { path: { requestId } }, signal })),
    }),
  /** What a step was given and gave (ADR-0057); `at` changes with the step, so a finished step is read again. */
  stepData: (requestId: string, stepId: string, at: string) =>
    queryOptions({
      queryKey: keys.requests.stepData(requestId, stepId, at),
      queryFn: ({ signal }) =>
        call(api.GET("/requests/{requestId}/steps/{stepId}/data", { params: { path: { requestId, stepId } }, signal })),
    }),
  /** What the requested version builds (ADR-0032): it never changes, so it is read once. */
  requestTopology: (requestId: string) =>
    queryOptions({
      queryKey: keys.requests.topology(requestId),
      queryFn: ({ signal }) =>
        call(api.GET("/requests/{requestId}/topology", { params: { path: { requestId } }, signal })),
      staleTime: Infinity,
    }),
  /** Approvals the caller may decide (pending), or decided ones. */
  approvals: (status: ApprovalStatus[] = ["pending"]) =>
    queryOptions({
      queryKey: keys.approvals.list(status),
      queryFn: ({ signal }) =>
        call(
          api.GET("/approvals", { params: { query: { status: csv(status) as ApprovalStatus[], limit: 200 } }, signal }),
        ),
    }),
  approval: (id: string) =>
    queryOptions({
      queryKey: keys.approvals.detail(id),
      queryFn: ({ signal }) =>
        call(api.GET("/approvals/{approvalId}", { params: { path: { approvalId: id } }, signal })),
    }),
  requestApprovals: (requestId: string) =>
    queryOptions({
      queryKey: keys.approvals.forRequest(requestId),
      queryFn: ({ signal }) =>
        call(api.GET("/requests/{requestId}/approvals", { params: { path: { requestId } }, signal })),
    }),
  deployments: (f: DeploymentFilters) =>
    infiniteQueryOptions({
      queryKey: keys.deployments.list(f),
      initialPageParam: undefined as string | undefined,
      queryFn: ({ pageParam, signal }) =>
        call(
          api.GET("/deployments", {
            params: {
              query: {
                project_id: f.projectId || undefined,
                status: csv(f.status),
                environment: csv(f.environment),
                item: csv(f.item),
                template: csv(f.template),
                owner: f.owner,
                q: f.q,
                lease_expires_before: f.leaseExpiresBefore,
                sort: f.sort,
                limit: f.limit ?? 50,
                cursor: pageParam,
              },
            },
            signal,
          }),
        ),
      getNextPageParam: (last) => last.next_cursor ?? undefined,
    }),
  /** The items, templates and environments the deployments have, to filter them by. */
  deploymentFacets: (projectId: string) =>
    queryOptions({
      queryKey: keys.deployments.facets(projectId),
      queryFn: ({ signal }) =>
        call(api.GET("/deployments/facets", { params: { query: { project_id: projectId || undefined } }, signal })),
      staleTime: 60_000,
    }),
  vms: (f: VmFilters) =>
    infiniteQueryOptions({
      queryKey: keys.vms.list(f),
      initialPageParam: undefined as string | undefined,
      queryFn: ({ pageParam, signal }) =>
        call(
          api.GET("/resources/vms", {
            params: {
              query: {
                project_id: f.projectId || undefined,
                environment: csv(f.environment),
                power_state: csv(f.powerState),
                vcenter: f.vcenter,
                cluster: f.cluster,
                network: f.network,
                q: f.q,
                sort: f.sort,
                limit: f.limit ?? 50,
                cursor: pageParam,
              },
            },
            signal,
          }),
        ),
      getNextPageParam: (last) => last.next_cursor ?? undefined,
    }),
  /** The vCenters, clusters and networks the VMs are in, to filter them by. */
  vmFacets: (projectId: string) =>
    queryOptions({
      queryKey: keys.vms.facets(projectId),
      queryFn: ({ signal }) =>
        call(api.GET("/resources/vms/facets", { params: { query: { project_id: projectId || undefined } }, signal })),
      staleTime: 60_000,
    }),
  savedViews: (list: ViewList) =>
    queryOptions({
      queryKey: keys.views(list),
      queryFn: ({ signal }) => call(api.GET("/me/views", { params: { query: { list } }, signal })),
    }),
  deployment: (id: string) =>
    queryOptions({
      queryKey: keys.deployments.detail(id),
      queryFn: ({ signal }) =>
        call(api.GET("/deployments/{deploymentId}", { params: { path: { deploymentId: id } }, signal })),
    }),
  deploymentActions: (id: string) =>
    queryOptions({
      queryKey: keys.deployments.actions(id),
      queryFn: ({ signal }) =>
        call(api.GET("/deployments/{deploymentId}/actions", { params: { path: { deploymentId: id } }, signal })),
    }),
  deploymentHistory: (id: string) =>
    queryOptions({
      queryKey: keys.deployments.history(id),
      queryFn: ({ signal }) =>
        call(
          api.GET("/deployments/{deploymentId}/history", {
            params: { path: { deploymentId: id }, query: { limit: 50 } },
            signal,
          }),
        ),
    }),
};
