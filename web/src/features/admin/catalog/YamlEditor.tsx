// A YAML editor (CodeMirror 6, loaded lazily) whose problems show on their
// lines: the catalog admin's view of a version's definition (ADR-0019).
//
// The composite designer edits the same text through its handle (apply):
// each of its edits is one step in the editor's history, beside what is
// typed, so Undo and Redo (here, Ctrl+Z, or the designer's buttons) go back
// through both in the order they were made (ADR-0032).
import {
  defaultKeymap,
  history,
  historyKeymap,
  isolateHistory,
  redo,
  redoDepth,
  undo,
  undoDepth,
} from "@codemirror/commands";
import { yaml } from "@codemirror/lang-yaml";
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { lintGutter, setDiagnostics, type Diagnostic } from "@codemirror/lint";
import { Compartment, EditorState, Transaction } from "@codemirror/state";
import { EditorView, keymap, lineNumbers } from "@codemirror/view";
import { tags as t } from "@lezer/highlight";
import { type Ref, useEffect, useImperativeHandle, useRef } from "react";

export interface LineProblem {
  line: number;
  message: string;
}

export interface EditorHistory {
  canUndo: boolean;
  canRedo: boolean;
}

export interface YamlEditorHandle {
  /** The text with a change made elsewhere: written as one step of the history, the change scrolled into view. */
  apply: (next: string) => void;
  undo: () => boolean;
  redo: () => boolean;
}

export interface YamlEditorProps {
  value: string;
  onChange?: (v: string) => void;
  problems?: LineProblem[];
  readOnly?: boolean;
  label: string;
  ref?: Ref<YamlEditorHandle>;
  /** Whether there is something to undo or redo, each time that changes. */
  onHistory?: (h: EditorHistory) => void;
}

/** The smallest change from one text to the other: what they don't share at the start and the end. */
export function minimalChange(from: string, to: string): { from: number; to: number; insert: string } {
  let a = 0;
  while (a < from.length && a < to.length && from[a] === to[a]) a++;
  let b = 0;
  while (b < from.length - a && b < to.length - a && from[from.length - 1 - b] === to[to.length - 1 - b]) b++;
  return { from: a, to: from.length - b, insert: to.slice(a, to.length - b) };
}

const theme = EditorView.theme({
  "&": { backgroundColor: "var(--surface)", color: "var(--fg)", fontSize: "12px", height: "100%" },
  ".cm-content": { fontFamily: "var(--font-mono, ui-monospace, monospace)", caretColor: "var(--fg)" },
  ".cm-gutters": { backgroundColor: "var(--sunken)", color: "var(--fg-faint)", borderRight: "1px solid var(--line)" },
  "&.cm-focused": { outline: "2px solid color-mix(in srgb, var(--accent) 40%, transparent)" },
  ".cm-scroller": { overflow: "auto" },
});

// The app's colours, which keep their contrast in both themes (S14.8);
// CodeMirror's default style is made for a white background.
const highlight = HighlightStyle.define([
  { tag: [t.propertyName, t.definition(t.propertyName)], color: "var(--accent)" },
  { tag: [t.string, t.special(t.string)], color: "var(--ok)" },
  { tag: [t.number, t.bool, t.null, t.atom], color: "var(--run)" },
  { tag: [t.keyword, t.meta, t.typeName, t.labelName], color: "var(--warn)" },
  { tag: t.comment, color: "var(--fg-muted)", fontStyle: "italic" },
  { tag: t.invalid, color: "var(--bad)" },
]);

export default function YamlEditor({
  value,
  onChange,
  problems = [],
  readOnly,
  label,
  ref,
  onHistory,
}: YamlEditorProps) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const changed = useRef(onChange);
  changed.current = onChange;
  const historyChanged = useRef(onHistory);
  historyChanged.current = onHistory;
  const said = useRef<EditorHistory>({ canUndo: false, canRedo: false });
  const historyPart = useRef(new Compartment());

  useImperativeHandle(
    ref,
    () => ({
      apply(next) {
        const v = view.current;
        if (!v) return;
        const change = minimalChange(v.state.doc.toString(), next);
        if (change.from === change.to && !change.insert) return;
        v.dispatch({
          changes: change,
          annotations: [isolateHistory.of("full"), Transaction.userEvent.of("input.designer")],
          effects: EditorView.scrollIntoView(change.from, { y: "nearest" }),
        });
      },
      undo: () => (view.current ? undo(view.current) : false),
      redo: () => (view.current ? redo(view.current) : false),
    }),
    [],
  );

  useEffect(() => {
    if (!host.current) return;
    const v = new EditorView({
      parent: host.current,
      state: EditorState.create({
        doc: value,
        extensions: [
          lineNumbers(),
          historyPart.current.of(history()),
          keymap.of([...defaultKeymap, ...historyKeymap]),
          yaml(),
          syntaxHighlighting(highlight),
          lintGutter(),
          theme,
          EditorState.readOnly.of(Boolean(readOnly)),
          // Focusable when read-only too, to scroll it from the keyboard.
          EditorView.contentAttributes.of({ "aria-label": label, tabindex: "0" }),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) changed.current?.(u.state.doc.toString());
            const h = { canUndo: undoDepth(u.state) > 0, canRedo: redoDepth(u.state) > 0 };
            if (h.canUndo !== said.current.canUndo || h.canRedo !== said.current.canRedo) {
              said.current = h;
              historyChanged.current?.(h);
            }
          }),
        ],
      }),
    });
    view.current = v;
    // A new editor has no history yet.
    said.current = { canUndo: false, canRedo: false };
    historyChanged.current?.(said.current);
    return () => v.destroy();
    // The editor is made once; value changes from outside are applied below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [readOnly, label]);

  // A value from outside (loaded, reset, saved) is not an edit, and what
  // was undone before it can't be undone after it: the history starts again.
  useEffect(() => {
    const v = view.current;
    if (v && v.state.doc.toString() !== value) {
      v.dispatch({
        changes: { from: 0, to: v.state.doc.length, insert: value },
        annotations: Transaction.addToHistory.of(false),
        effects: historyPart.current.reconfigure([]),
      });
      v.dispatch({ effects: historyPart.current.reconfigure(history()) });
    }
  }, [value, readOnly, label]);

  useEffect(() => {
    const v = view.current;
    if (!v) return;
    const doc = v.state.doc;
    const diagnostics: Diagnostic[] = problems
      .filter((p) => p.line >= 1 && p.line <= doc.lines)
      .map((p) => {
        const line = doc.line(p.line);
        return { from: line.from, to: line.to, severity: "error", message: p.message };
      });
    v.dispatch(setDiagnostics(v.state, diagnostics));
    // Again when the view is made anew (readOnly or label changed).
  }, [problems, readOnly, label]);

  return <div ref={host} className="h-[min(70vh,640px)] overflow-hidden rounded-md border border-line-strong" />;
}
