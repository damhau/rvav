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
  if (!found || statusChanged) void qc.invalidateQueries({ queryKey: listKey });
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
      qc.setQueryData<ServiceRequest>(keys.requests.detail(r.number), r);
      qc.setQueryData<ServiceRequest>(keys.requests.detail(r.id), r);
      patchLists(qc, keys.requests.lists(), r);
      break;
    }
    case "request.step": {
      const { request_id, step, parent_step_id } = event.data;
      qc.setQueryData<{ items: Step[] }>(keys.requests.steps(request_id), (old) =>
        old ? { items: upsertStep(old.items, step, parent_step_id ?? null) } : old,
      );
      break;
    }
    case "request.log":
      qc.setQueryData(keys.requests.logHead(event.data.request_id, event.data.step_id), event.data.next_offset);
      break;
    case "deployment.updated": {
      const d: Deployment = event.data;
      qc.setQueryData<DeploymentDetail>(keys.deployments.detail(d.id), (old) => (old ? { ...old, ...d } : old));
      patchLists(qc, keys.deployments.lists(), d);
      break;
    }
    case "resource.updated": {
      const { deployment_id, resource } = event.data;
      qc.setQueryData<DeploymentDetail>(keys.deployments.detail(deployment_id), (old) => {
        if (!old) return old;
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
    case "resync":
      void qc.invalidateQueries();
      break;
    case "heartbeat":
      break;
  }
}
