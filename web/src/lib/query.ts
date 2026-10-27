import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api, call } from "./api/client";
import type { ApprovalStatus, CatalogItemType, DeploymentStatus, EnvironmentKey, RequestStatus } from "./api/types";

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

export interface DeploymentFilters {
  projectId: string;
  status?: DeploymentStatus[];
  environment?: EnvironmentKey[];
  owner?: string;
  q?: string;
  leaseExpiresBefore?: string;
  sort?: "name" | "-name" | "created_at" | "-created_at" | "lease_expires_at" | "-lease_expires_at";
  limit?: number;
}

// Query key factories: one place per resource, so invalidation stays precise.
export const keys = {
  me: ["me"] as const,
  ipamReconcile: ["admin", "ipam", "reconcile"] as const,
  environments: ["environments"] as const,
  project: (id: string) => ["projects", id] as const,
  quota: (id: string, environment?: string) => ["projects", id, "quota", environment ?? "all"] as const,
  catalog: {
    all: ["catalog"] as const,
    categories: (projectId: string) => ["catalog", "categories", projectId] as const,
    lists: () => ["catalog", "list"] as const,
    list: (f: CatalogFilters) => ["catalog", "list", f] as const,
    item: (key: string, projectId: string) => ["catalog", "item", key, projectId] as const,
    form: (key: string, projectId: string) => ["catalog", "form", key, projectId] as const,
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
    logHead: (requestId: string, stepId: string) => ["requests", "log-head", requestId, stepId] as const,
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
    history: (id: string) => ["deployments", "history", id] as const,
  },
};

const csv = <T extends string>(v?: T[]) => (v && v.length ? (v as string[]) : undefined);

export const q = {
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
  form: (key: string, projectId: string) =>
    queryOptions({
      queryKey: keys.catalog.form(key, projectId),
      queryFn: ({ signal }) =>
        call(
          api.GET("/catalog/items/{itemId}/form", {
            params: { path: { itemId: key }, query: { project_id: projectId } },
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
                status: csv(f.status) as RequestStatus[] | undefined,
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
                project_id: f.projectId,
                status: csv(f.status) as DeploymentStatus[] | undefined,
                environment: csv(f.environment),
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
