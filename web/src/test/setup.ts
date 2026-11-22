/* eslint-disable @typescript-eslint/unbound-method -- polyfills only check whether a method exists */
import "@testing-library/jest-dom/vitest";
import { cleanup, configure } from "@testing-library/react";
import { afterAll, afterEach, beforeAll, beforeEach } from "vitest";
import { i18n, initI18n, LOCALE_KEY } from "@/lib/i18n";
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

// The tests read the English texts, whatever the machine's language; a key
// without an English text fails the test that shows it.
await initI18n({
  lng: "en",
  saveMissing: true,
  missingKeyHandler: (_lngs, ns, key) => {
    throw new Error(`No English text for ${ns}:${key}`);
  },
});

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
beforeEach(() => resetMocks());
afterEach(async () => {
  cleanup();
  server.resetHandlers();
  localStorage.removeItem(LOCALE_KEY);
  if (i18n.language !== "en") await i18n.changeLanguage("en");
});
afterAll(() => server.close());
