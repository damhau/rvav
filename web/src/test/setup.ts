/* eslint-disable @typescript-eslint/unbound-method -- polyfills only check whether a method exists */
import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterAll, afterEach, beforeAll, beforeEach } from "vitest";
import { resetMocks, server } from "@/mocks/node";

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
