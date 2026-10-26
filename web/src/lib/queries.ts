import { queryOptions, type QueryClient } from "@tanstack/react-query";
import * as api from "@/mock/api";
import type { Env } from "./types";

// Query key factories: one place per resource so invalidation stays precise.
export const keys = {
  projects: ["projects"] as const,
  catalog: ["catalog"] as const,
  catalogItem: (slug: string) => ["catalog", slug] as const,
  deployments: ["deployments"] as const,
  deployment: (id: string) => ["deployments", id] as const,
  requests: ["requests"] as const,
  request: (id: string) => ["requests", id] as const,
  approvals: ["approvals"] as const,
  audit: ["audit"] as const,
  clusters: (env: Env) => ["inventory", "clusters", env] as const,
  datastores: (cluster: string) => ["inventory", "datastores", cluster] as const,
  networks: (env: Env) => ["inventory", "networks", env] as const,
  nextName: (project: string, env: Env, role: string) => ["naming", project, env, role] as const,
  freeIp: (network: string) => ["ipam", "free", network] as const,
};

export const q = {
  projects: () => queryOptions({ queryKey: keys.projects, queryFn: api.getProjects, staleTime: 5 * 60_000 }),
  catalog: () => queryOptions({ queryKey: keys.catalog, queryFn: api.getCatalog }),
  catalogItem: (slug: string) => queryOptions({ queryKey: keys.catalogItem(slug), queryFn: () => api.getCatalogItem(slug) }),
  deployments: () => queryOptions({ queryKey: keys.deployments, queryFn: api.getDeployments }),
  deployment: (id: string) => queryOptions({ queryKey: keys.deployment(id), queryFn: () => api.getDeployment(id) }),
  requests: () => queryOptions({ queryKey: keys.requests, queryFn: api.getRequests }),
  request: (id: string) => queryOptions({ queryKey: keys.request(id), queryFn: () => api.getRequest(id) }),
  approvals: () => queryOptions({ queryKey: keys.approvals, queryFn: api.getApprovals }),
  audit: () => queryOptions({ queryKey: keys.audit, queryFn: api.getAudit }),
  clusters: (env: Env) => queryOptions({ queryKey: keys.clusters(env), queryFn: () => api.getClusters(env), staleTime: 60_000 }),
  datastores: (cluster: string) =>
    queryOptions({ queryKey: keys.datastores(cluster), queryFn: () => api.getDatastores(cluster), enabled: !!cluster, staleTime: 60_000 }),
  networks: (env: Env) => queryOptions({ queryKey: keys.networks(env), queryFn: () => api.getNetworks(env), staleTime: 60_000 }),
  nextName: (project: string, env: Env, role: string) =>
    queryOptions({ queryKey: keys.nextName(project, env, role), queryFn: () => api.getNextName(project, env, role) }),
  freeIp: (network: string) => queryOptions({ queryKey: keys.freeIp(network), queryFn: () => api.getFreeIp(network), enabled: !!network }),
};

// Bridges the live event stream into the cache, the way the SSE client will.
export function connectLive(client: QueryClient) {
  return api.events.subscribe((e) => {
    switch (e.type) {
      case "request.updated":
        client.invalidateQueries({ queryKey: keys.request(e.id) });
        client.invalidateQueries({ queryKey: keys.requests, exact: true });
        break;
      case "deployment.updated":
        client.invalidateQueries({ queryKey: keys.deployment(e.id) });
        client.invalidateQueries({ queryKey: keys.deployments, exact: true });
        break;
      case "approval.changed":
        client.invalidateQueries({ queryKey: keys.approvals });
        client.invalidateQueries({ queryKey: keys.requests });
        break;
    }
  });
}
