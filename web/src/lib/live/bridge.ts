// Applies live events to the TanStack Query cache, so screens update without polling.
import type { InfiniteData, QueryClient, QueryKey } from "@tanstack/react-query";
import type {
  Deployment,
  DeploymentDetail,
  LiveEvent,
  NotificationEvent,
  Page,
  ServiceRequest,
  Step,
} from "@/lib/api/types";
import { keys } from "@/lib/query";

type ListData<T> = Page<T> | InfiniteData<Page<T>>;

const isInfinite = <T>(d: ListData<T>): d is InfiniteData<Page<T>> => "pages" in d;

const fetching = (qc: QueryClient, key: QueryKey) =>
  qc
    .getQueryCache()
    .findAll({ queryKey: key })
    .some((q) => q.state.fetchStatus === "fetching");

/**
 * Patch a cached query. While its fetch is in flight the response would
 * overwrite the patch with an older snapshot, so refetch instead.
 */
function patch<T>(qc: QueryClient, key: QueryKey, fn: (old: T) => T) {
  if (fetching(qc, key)) {
    void qc.invalidateQueries({ queryKey: key, exact: true });
    return;
  }
  qc.setQueryData<T>(key, (old) => (old === undefined ? old : fn(old)));
}

function replace<T>(qc: QueryClient, key: QueryKey, data: T) {
  if (fetching(qc, key)) void qc.invalidateQueries({ queryKey: key, exact: true });
  else qc.setQueryData<T>(key, data);
}

/**
 * Replace an item in every cached list under `listKey`. If a list doesn't
 * contain it, or its status changed (filters may no longer match), refetch.
 */
function patchLists<T extends { id: string; status: string }>(qc: QueryClient, listKey: QueryKey, item: T) {
  let found = false;
  let statusChanged = false;
  const replace = (items: T[]) =>
    items.map((x) => {
      if (x.id !== item.id) return x;
      found = true;
      if (x.status !== item.status) statusChanged = true;
      return { ...x, ...item };
    });
  qc.setQueriesData<ListData<T>>({ queryKey: listKey }, (old) => {
    if (!old) return old;
    if (isInfinite(old)) return { ...old, pages: old.pages.map((p) => ({ ...p, items: replace(p.items) })) };
    return { ...old, items: replace(old.items) };
  });
  if (!found || statusChanged || fetching(qc, listKey)) void qc.invalidateQueries({ queryKey: listKey });
}

/** Map over a step tree; returns null when `fn` changed nothing. */
function mapTree(list: Step[], fn: (s: Step) => Step | null): Step[] | null {
  let changed = false;
  const out = list.map((s) => {
    const own = fn(s);
    if (own) {
      changed = true;
      return own;
    }
    const children = s.children.length ? mapTree(s.children, fn) : null;
    if (children) {
      changed = true;
      return { ...s, children };
    }
    return s;
  });
  return changed ? out : null;
}

/** Replace a step by ID anywhere in the tree, or insert it under its parent (or at the end). */
export function upsertStep(steps: Step[], step: Step, parentId: string | null): Step[] {
  const replaced = mapTree(steps, (s) => (s.id === step.id ? step : null));
  if (replaced) return replaced;
  const inserted = parentId
    ? mapTree(steps, (s) => (s.id === parentId ? { ...s, children: [...s.children, step] } : null))
    : null;
  return inserted ?? [...steps, step];
}

export function applyEvent(qc: QueryClient, event: LiveEvent, notify?: (n: NotificationEvent["data"]) => void) {
  switch (event.type) {
    case "request.updated": {
      const r = event.data;
      const before =
        qc.getQueryData<ServiceRequest>(keys.requests.detail(r.number)) ??
        qc.getQueryData<ServiceRequest>(keys.requests.detail(r.id));
      replace<ServiceRequest>(qc, keys.requests.detail(r.number), r);
      replace<ServiceRequest>(qc, keys.requests.detail(r.id), r);
      patchLists(qc, keys.requests.lists(), r);
      // Approved, rejected or cancelled: its approval history changed too.
      if (before && before.status !== r.status)
        void qc.invalidateQueries({ queryKey: keys.approvals.forRequest(r.id) });
      // A day-2 request started or ended: the deployment's history changed.
      if (r.deployment_id && before?.status !== r.status)
        void qc.invalidateQueries({ queryKey: keys.deployments.history(r.deployment_id) });
      break;
    }
    case "request.step": {
      const { request_id, step, parent_step_id } = event.data;
      patch<{ items: Step[] }>(qc, keys.requests.steps(request_id), (old) => ({
        items: upsertStep(old.items, step, parent_step_id ?? null),
      }));
      break;
    }
    case "request.log":
      qc.setQueryData(keys.requests.logHead(event.data.request_id, event.data.step_id), event.data.next_offset);
      break;
    case "deployment.updated": {
      const d: Deployment = event.data;
      const before = qc.getQueryData<DeploymentDetail>(keys.deployments.detail(d.id));
      patch<DeploymentDetail>(qc, keys.deployments.detail(d.id), (old) => ({ ...old, ...d }));
      patchLists(qc, keys.deployments.lists(), d);
      // What may run, and what ran, follow the deployment's state.
      void qc.invalidateQueries({ queryKey: keys.deployments.actions(d.id) });
      void qc.invalidateQueries({ queryKey: keys.deployments.history(d.id) });
      // Deleted resources arrive only as a status change: read them again.
      if (before && before.status !== d.status) void qc.invalidateQueries({ queryKey: keys.deployments.detail(d.id) });
      break;
    }
    case "resource.updated": {
      const { deployment_id, resource } = event.data;
      void qc.invalidateQueries({ queryKey: keys.deployments.actions(deployment_id) });
      patch<DeploymentDetail>(qc, keys.deployments.detail(deployment_id), (old) => {
        const exists = old.resources.some((r) => r.id === resource.id);
        return {
          ...old,
          resources: exists
            ? old.resources.map((r) => (r.id === resource.id ? resource : r))
            : [...old.resources, resource],
        };
      });
      break;
    }
    case "notification":
      notify?.(event.data);
      break;
    case "approval.updated":
      // Only IDs arrive: refetch the inbox, the level and the request's history.
      void qc.invalidateQueries({ queryKey: keys.approvals.lists() });
      void qc.invalidateQueries({ queryKey: keys.approvals.detail(event.data.approval_id) });
      void qc.invalidateQueries({ queryKey: keys.approvals.forRequest(event.data.request_id) });
      break;
    case "resync":
      void qc.invalidateQueries();
      break;
    case "heartbeat":
      break;
  }
}
