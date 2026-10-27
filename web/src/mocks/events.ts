// Mock event bus behind GET /events: keeps the last events so a reconnecting
// client can catch up with Last-Event-ID, like the real server.
import type { LiveEvent } from "@/lib/api/types";

export interface Envelope {
  event: LiveEvent;
  projectId?: string;
  requestId?: string;
  requestNumber?: string;
  deploymentId?: string;
  userId?: string;
  /** Approval events go to these users (the level's approvers). */
  userIds?: string[];
}

type Listener = (e: Envelope) => void;

const BUFFER = 500;
let seq = 0;
let buffer: Envelope[] = [];
const listeners = new Set<Listener>();

export const bus = {
  nextId() {
    seq += 1;
    return `evt_${String(seq).padStart(8, "0")}`;
  },
  publish(env: Envelope) {
    buffer.push(env);
    if (buffer.length > BUFFER) buffer = buffer.slice(-BUFFER);
    listeners.forEach((fn) => fn(env));
  },
  subscribe(fn: Listener) {
    listeners.add(fn);
    return () => {
      listeners.delete(fn);
    };
  },
  /** Events after `id`, or null when `id` is no longer in the buffer (the client must resync). */
  since(id: string): Envelope[] | null {
    const i = buffer.findIndex((e) => e.event.id === id);
    if (i === -1) return buffer.length === 0 || id > (buffer.at(-1)?.event.id ?? "") ? [] : null;
    return buffer.slice(i + 1);
  },
  reset() {
    seq = 0;
    buffer = [];
    listeners.clear();
  },
};

/** Does this event belong on a stream opened with these topics? */
export function matches(env: Envelope, topics: string[], projectId: string | null, viewerId?: string): boolean {
  const t = env.event.type;
  const inProject = !projectId || env.projectId === projectId;
  for (const topic of topics) {
    if (topic === "requests" && t.startsWith("request.") && inProject) return true;
    if (topic === "deployments" && (t === "deployment.updated" || t === "resource.updated") && inProject) return true;
    if (topic === "notifications" && t === "notification") return true;
    if (topic === "approvals" && t === "approval.updated" && viewerId && env.userIds?.includes(viewerId)) return true;
    if (topic.startsWith("request:")) {
      const key = topic.slice("request:".length);
      if (t.startsWith("request.") && (key === env.requestId || key === env.requestNumber)) return true;
    }
    if (topic.startsWith("deployment:")) {
      const key = topic.slice("deployment:".length);
      if ((t === "deployment.updated" || t === "resource.updated") && key === env.deploymentId) return true;
    }
  }
  return false;
}
