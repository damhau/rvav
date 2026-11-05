import { EditorView } from "@codemirror/view";
import { act, render } from "@testing-library/react";
import { createRef } from "react";
import { describe, expect, it } from "vitest";
import YamlEditor, { type YamlEditorHandle } from "./YamlEditor";

describe("YamlEditor's handle", () => {
  // The editor draws only the lines near what is in view: Undo and Redo
  // scroll to the selection they put back, so it must be on the change.
  it("puts the cursor on a change, so Undo and Redo show the lines they change", () => {
    const text = "steps:\n" + "  - key: a\n".repeat(200);
    const ref = createRef<YamlEditorHandle>();
    const { container } = render(<YamlEditor value={text} label="Definition" ref={ref} />);
    const view = EditorView.findFromDOM(container.querySelector<HTMLElement>(".cm-editor")!)!;
    expect(view.state.selection.main.head).toBe(0);

    const at = text.length - "a\n".length;
    act(() => ref.current!.apply(text.slice(0, at) + "last\n"));
    expect(view.state.doc.toString().endsWith("  - key: last\n")).toBe(true);
    expect(view.state.selection.main.head).toBe(at);

    act(() => void ref.current!.undo());
    expect(view.state.doc.toString()).toBe(text);
    expect(view.state.selection.main.head).toBe(at);

    act(() => void ref.current!.redo());
    expect(view.state.doc.toString().endsWith("  - key: last\n")).toBe(true);
    expect(view.state.selection.main.head).toBe(at);
  });
});
