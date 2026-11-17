/* eslint-disable @typescript-eslint/unbound-method -- polyfills only check whether a method exists */
import "@testing-library/jest-dom/vitest";
import { cleanup, configure } from "@testing-library/react";
import { afterAll, afterEach, beforeAll, beforeEach } from "vitest";
import { resetMocks, server } from "@/mocks/node";

// The whole suite runs its files in parallel, and under that load a page can
// take longer than Testing Library's default second to render.
configure({ asyncUtilTimeout: 5000 });

// jsdom gaps that Radix and cmdk rely on.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;
Element.prototype.scrollIntoView ??= function scrollIntoView() {};
window.scrollTo = () => {};
Element.prototype.hasPointerCapture ??= () => false;
Element.prototype.releasePointerCapture ??= () => {};
// React Flow measures its edges' labels.
(SVGElement.prototype as unknown as { getBBox?: () => DOMRect }).getBBox ??= () => new DOMRect(0, 0, 0, 0);
// CodeMirror measures its text.
Range.prototype.getClientRects ??= () => [] as unknown as DOMRectList;
Range.prototype.getBoundingClientRect ??= () => new DOMRect(0, 0, 0, 0);
window.matchMedia ??= (query: string) =>
  ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }) as MediaQueryList;

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
beforeEach(() => resetMocks());
afterEach(() => {
  cleanup();
  server.resetHandlers();
});
afterAll(() => server.close());
