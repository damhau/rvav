import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useSearchBox } from "./hooks";

describe("useSearchBox", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  function box(initial?: string) {
    const searches: (string | undefined)[] = [];
    const initialProps: { search?: string; cleared?: number } = { search: initial };
    const view = renderHook(
      ({ search, cleared }: { search?: string; cleared?: number }) =>
        useSearchBox(
          search,
          (next) => {
            searches.push(next);
          },
          { cleared },
        ),
      { initialProps },
    );
    return {
      view,
      searches,
      text: () => view.result.current[0],
      type: (t: string) => act(() => view.result.current[1](t)),
    };
  }

  it("searches once typing pauses, trimmed", () => {
    const { searches, text, type } = box();
    type("web");
    type("web ");
    expect(text()).toBe("web ");
    expect(searches).toEqual([]);
    act(() => {
      vi.advanceTimersByTime(250);
    });
    expect(searches).toEqual(["web"]);
  });

  it("keeps what is typed after its search, while that search lands", () => {
    const { view, text, type } = box();
    type("web");
    act(() => {
      vi.advanceTimersByTime(250);
    });
    type("web-p");
    view.rerender({ search: "web" });
    expect(text()).toBe("web-p");
  });

  it("shows a search changed elsewhere: Back, or Clear the filters", () => {
    const { view, searches, text, type } = box("web");
    type("web-prd");
    act(() => {
      vi.advanceTimersByTime(250);
    });
    view.rerender({ search: "web-prd" });
    view.rerender({ search: undefined });
    expect(text()).toBe("");
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(searches).toEqual(["web-prd"]);
  });

  it("drops what is typed and not yet searched when the filters are cleared", () => {
    const { view, searches, text, type } = box();
    type("ab");
    view.rerender({ search: undefined, cleared: 1 });
    expect(text()).toBe("");
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(searches).toEqual([]);
  });
});
