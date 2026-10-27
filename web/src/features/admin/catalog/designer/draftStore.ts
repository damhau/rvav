// The working copy of each draft the form designer edits (ADR-0019). It
// lives outside React, so it survives switching tabs: every change is saved
// after a pause, one save at a time, each on the revision it started from;
// a save the server refuses keeps the copy (and its problems) until the
// author fixes it, and a draft changed elsewhere is a conflict, not lost.
import { useSyncExternalStore } from "react";
import { isApiError } from "@/lib/api/client";
import type { FieldError } from "@/lib/api/types";
import type { Def } from "./model";

export type SaveState = "saved" | "saving" | "invalid" | "conflict" | "failed";

export interface DraftCopy {
  def: Def;
  /** The server's revision the copy builds on. */
  revision: number;
  /** The copy differs from what the server has. */
  dirty: boolean;
  state: SaveState;
  /** The server's problems with the copy (422). */
  problems: FieldError[];
  /** Why the last save failed otherwise. */
  error?: string;
}

/** Saves a definition on a base revision and gives the new revision. */
export type Saver = (def: Def, baseRevision: number) => Promise<number>;

const pause = 600;
const copies = new Map<string, DraftCopy>();
const savers = new Map<string, Saver>();
const timers = new Map<string, ReturnType<typeof setTimeout>>();
const inFlight = new Set<string>();
const listeners = new Set<() => void>();

function set(id: string, copy: DraftCopy | undefined) {
  if (copy) copies.set(id, copy);
  else copies.delete(id);
  for (const l of listeners) l();
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

/** The working copy of a draft, if one is open. */
export function useDraftCopy(id: string | undefined): DraftCopy | undefined {
  return useSyncExternalStore(subscribe, () => (id ? copies.get(id) : undefined));
}

export function draftCopy(id: string): DraftCopy | undefined {
  return copies.get(id);
}

/**
 * Opens a draft from the server's version: a copy with unsaved changes is
 * kept, any other is replaced when the server has a newer revision.
 */
export function openDraft(id: string, def: Def, revision: number): DraftCopy {
  const current = copies.get(id);
  if (current && (current.dirty || current.revision >= revision)) return current;
  const copy: DraftCopy = { def, revision, dirty: false, state: "saved", problems: [] };
  set(id, copy);
  return copy;
}

/** Replaces the copy with what the server now has (after the YAML view saved, or to drop a conflict). */
export function resetDraft(id: string, def: Def, revision: number) {
  clearTimeout(timers.get(id));
  set(id, { def, revision, dirty: false, state: "saved", problems: [] });
}

/** Forgets a draft (published, discarded). */
export function closeDraft(id: string) {
  clearTimeout(timers.get(id));
  savers.delete(id);
  set(id, undefined);
}

/** Records an edit and saves it after a pause. */
export function editDraft(id: string, def: Def, save: Saver) {
  const current = copies.get(id);
  if (!current) return;
  savers.set(id, save);
  set(id, { ...current, def, dirty: true, state: current.state === "conflict" ? "conflict" : "saving" });
  if (current.state === "conflict") return; // the author chooses first
  clearTimeout(timers.get(id));
  timers.set(
    id,
    setTimeout(() => void flush(id), pause),
  );
}

/** Saves the copy now, over whatever the server has (the author's choice in a conflict). */
export function saveOver(id: string, serverRevision: number) {
  const current = copies.get(id);
  if (!current) return;
  set(id, { ...current, revision: serverRevision, state: "saving" });
  void flush(id);
}

/** Saves what is pending now (leaving the designer). */
export function flushDraft(id: string) {
  if (!timers.has(id)) return;
  clearTimeout(timers.get(id));
  void flush(id);
}

async function flush(id: string) {
  timers.delete(id);
  const save = savers.get(id);
  const copy = copies.get(id);
  if (!save || !copy?.dirty || copy.state === "conflict") return;
  if (inFlight.has(id)) {
    // One save at a time: the next one goes when this one answers.
    timers.set(
      id,
      setTimeout(() => void flush(id), pause),
    );
    return;
  }
  inFlight.add(id);
  const sent = copy.def;
  try {
    const revision = await save(sent, copy.revision);
    const now = copies.get(id);
    if (!now) return;
    const same = now.def === sent;
    set(id, { ...now, revision, dirty: !same, state: same ? "saved" : "saving", problems: [], error: undefined });
    if (!same) void flush(id);
  } catch (e) {
    const now = copies.get(id);
    if (!now) return;
    if (isApiError(e) && e.status === 422) set(id, { ...now, state: "invalid", problems: e.fieldErrors });
    else if (isApiError(e) && (e.status === 409 || e.status === 404))
      set(id, { ...now, state: "conflict", error: e.problem.detail ?? e.problem.title });
    else set(id, { ...now, state: "failed", error: e instanceof Error ? e.message : String(e) });
  } finally {
    inFlight.delete(id);
  }
}

/** Some draft has changes the server doesn't have. */
export function anyUnsaved(): boolean {
  for (const c of copies.values()) if (c.dirty) return true;
  return false;
}

if (typeof window !== "undefined") {
  window.addEventListener("beforeunload", (e) => {
    if (anyUnsaved()) e.preventDefault();
  });
}
