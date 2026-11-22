// What the canvas's designers share (ADR-0032): the composite designer and
// the VM designer edit a draft's YAML on the canvas. Each edit is a function
// from the editor's text to a new text, written into the editor as one step
// of its history, so Undo and Redo (the buttons, Ctrl+Z, and Ctrl+Shift+Z or
// Ctrl+Y outside the text fields) go back through typed and designed changes
// alike. The selected node's details are its editor, under the canvas. What
// an edit did is said in a live region, for who doesn't see the canvas.
import { Redo2, Undo2 } from "lucide-react";
import { type ReactNode, type RefObject, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import type { FieldError } from "@/lib/api/types";

export interface DesignHistory {
  canUndo: boolean;
  canRedo: boolean;
}

/** The draft a designer edits: the editor's text, and its history. */
export interface Design {
  /** The editor's text now. */
  text: string;
  /** Writes an edit into the editor, as one step of its history. */
  onEdit: (next: string) => void;
  history: DesignHistory;
  undo: () => void;
  redo: () => void;
  /** The server's problems with a text (its last refusal), shown on their nodes while the text is that one. */
  serverProblems?: { text: string; errors: FieldError[] };
}

/**
 * Where an edit leaves the designer: the node to select, the element of its
 * details to focus (or the one to focus in the text it made), what to say
 * it did, and what to do with a refusal instead of a toast.
 */
export interface Then {
  select?: string | null;
  focus?: string | ((next: string) => string);
  says?: string | (() => string);
  onError?: (message: string) => void;
}

/** A node's details, as a panel under the canvas. */
export const panelClass = "flex flex-col gap-3 rounded-md border border-line bg-surface p-3 text-secondary";
export const legendClass = "mb-1.5 text-caption font-medium tracking-wide text-muted uppercase";

/** The selected node's details' heading: the focus goes there when its node is added or selected from elsewhere. */
export function PanelHeading({ badge, children }: { badge?: string; children: ReactNode }) {
  return (
    <h4 id="cd-heading" tabIndex={-1} className="flex items-baseline gap-1.5 text-body font-medium outline-none">
      {badge && <span className="text-badge tracking-wide text-faint uppercase">{badge}</span>}
      {children}
    </h4>
  );
}

/**
 * Focuses what an edit asked for, once its details are drawn. Given the
 * editor's text, it tries once in the details that text draws: an element
 * that isn't there then is dropped, so it can't take the focus later.
 */
export function useFocusNext(ref: RefObject<string | null>, text?: string) {
  const drawn = useRef(text);
  useEffect(() => {
    const changed = drawn.current !== text;
    drawn.current = text;
    const id = ref.current;
    if (!id) return;
    const el = document.getElementById(id);
    if (el) {
      ref.current = null;
      el.focus();
    } else if (changed) ref.current = null;
  });
}

/**
 * A designer's keys and focus: Ctrl+Z and Ctrl+Shift+Z (or Ctrl+Y) outside
 * the text fields, which have their own; and the focus kept in the designer
 * when what had it goes (an undo that removes the node whose details had
 * it), on the selected node's heading or node, else the graph's Tab stop.
 * `apply` makes an edit, then selects a node and focuses an element of its
 * details once they are drawn (they come with the graph, which follows the
 * text a moment later).
 */
export function useDesigner(design: Design, setSelected: (id: string | null) => void) {
  const { t } = useTranslation("canvas");
  const box = useRef<HTMLDivElement>(null);
  const focusNext = useRef<string | null>(null);
  // A said text again changes by a no-break space, so it's said again.
  const [said, setSaid] = useState({ text: "", n: 0 });
  const say = (text: string) => setSaid((s) => ({ text, n: s.n + 1 }));
  const { onEdit, history } = design;
  const undo = () => {
    if (!history.canUndo) return;
    design.undo();
    say(t("designer.undone"));
  };
  const redo = () => {
    if (!history.canRedo) return;
    design.redo();
    say(t("designer.redone"));
  };
  const keys = useRef({ undo, redo });
  keys.current = { undo, redo };

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey) || e.altKey) return;
      if ((e.target as HTMLElement).closest("input, textarea, [contenteditable=true]")) return;
      const k = e.key.toLowerCase();
      if (k === "z" && !e.shiftKey) {
        e.preventDefault();
        keys.current.undo();
      } else if ((k === "z" && e.shiftKey) || k === "y") {
        e.preventDefault();
        keys.current.redo();
      }
    };
    el.addEventListener("keydown", onKey);
    return () => el.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    let last: HTMLElement | null = null;
    const onFocus = (e: FocusEvent) => {
      last = e.target as HTMLElement;
    };
    const keep = new MutationObserver(() => {
      if (!last || last.isConnected) return;
      last = null;
      if (document.activeElement && document.activeElement !== document.body) return;
      const to =
        el.querySelector<HTMLElement>("#cd-heading") ??
        el.querySelector<HTMLElement>(".react-flow__node[aria-pressed=true]") ??
        el.querySelector<HTMLElement>(".react-flow__node[tabindex='0']") ??
        el;
      to.focus();
    });
    el.addEventListener("focusin", onFocus);
    keep.observe(el, { childList: true, subtree: true });
    return () => {
      el.removeEventListener("focusin", onFocus);
      keep.disconnect();
    };
  }, []);

  /** Makes an edit; false when it was refused (said in a toast, or to then.onError). */
  const apply = (make: () => string, then?: Then): boolean => {
    let next: string;
    try {
      next = make();
      onEdit(next);
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e);
      if (then?.onError) then.onError(message);
      else toast.error(message);
      return false;
    }
    if (then?.select !== undefined) setSelected(then.select);
    if (then?.focus) focusNext.current = typeof then.focus === "function" ? then.focus(next) : then.focus;
    if (then?.says) say(typeof then.says === "function" ? then.says() : then.says);
    return true;
  };

  const status = (
    <p role="status" className="sr-only">
      {said.text}
      {said.n % 2 ? "\u00a0" : ""}
    </p>
  );

  return { box, focusNext, apply, history: { ...design, undo, redo }, status };
}

/** Undo and Redo, over the canvas: they go back through the editor's history. */
export function HistoryButtons({ design }: { design: Design }) {
  const { t } = useTranslation("canvas");
  return (
    <>
      <Button
        size="sm"
        variant="quiet"
        disabled={!design.history.canUndo}
        onClick={design.undo}
        aria-keyshortcuts="Control+Z"
      >
        <Undo2 aria-hidden />
        {t("designer.undo")}
      </Button>
      <Button
        size="sm"
        variant="quiet"
        disabled={!design.history.canRedo}
        onClick={design.redo}
        aria-keyshortcuts="Control+Shift+Z Control+Y"
      >
        <Redo2 aria-hidden />
        {t("designer.redo")}
      </Button>
    </>
  );
}
