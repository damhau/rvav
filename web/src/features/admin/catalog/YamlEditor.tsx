// A YAML editor (CodeMirror 6, loaded lazily) whose problems show on their
// lines: the catalog admin's view of a version's definition (ADR-0019).
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { yaml } from "@codemirror/lang-yaml";
import { defaultHighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { lintGutter, setDiagnostics, type Diagnostic } from "@codemirror/lint";
import { EditorState, Transaction } from "@codemirror/state";
import { EditorView, keymap, lineNumbers } from "@codemirror/view";
import { useEffect, useRef } from "react";

export interface LineProblem {
  line: number;
  message: string;
}

export interface YamlEditorProps {
  value: string;
  onChange?: (v: string) => void;
  problems?: LineProblem[];
  readOnly?: boolean;
  label: string;
}

const theme = EditorView.theme({
  "&": { backgroundColor: "var(--surface)", color: "var(--fg)", fontSize: "12px", height: "100%" },
  ".cm-content": { fontFamily: "var(--font-mono, ui-monospace, monospace)", caretColor: "var(--fg)" },
  ".cm-gutters": { backgroundColor: "var(--sunken)", color: "var(--faint)", borderRight: "1px solid var(--line)" },
  "&.cm-focused": { outline: "2px solid color-mix(in srgb, var(--accent) 40%, transparent)" },
  ".cm-scroller": { overflow: "auto" },
});

export default function YamlEditor({ value, onChange, problems = [], readOnly, label }: YamlEditorProps) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const changed = useRef(onChange);
  changed.current = onChange;

  useEffect(() => {
    if (!host.current) return;
    const v = new EditorView({
      parent: host.current,
      state: EditorState.create({
        doc: value,
        extensions: [
          lineNumbers(),
          history(),
          keymap.of([...defaultKeymap, ...historyKeymap]),
          yaml(),
          syntaxHighlighting(defaultHighlightStyle),
          lintGutter(),
          theme,
          EditorState.readOnly.of(Boolean(readOnly)),
          EditorView.contentAttributes.of({ "aria-label": label }),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) changed.current?.(u.state.doc.toString());
          }),
        ],
      }),
    });
    view.current = v;
    return () => v.destroy();
    // The editor is made once; value changes from outside are applied below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [readOnly, label]);

  // A value from outside (loaded, reset) is not an edit: Ctrl+Z doesn't undo it.
  useEffect(() => {
    const v = view.current;
    if (v && v.state.doc.toString() !== value)
      v.dispatch({
        changes: { from: 0, to: v.state.doc.length, insert: value },
        annotations: Transaction.addToHistory.of(false),
      });
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
