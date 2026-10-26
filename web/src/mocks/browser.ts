// Starts the mock API in the browser. Imported dynamically, so real builds don't ship it.
import { getResponse } from "msw";
import { setupWorker } from "msw/browser";
import { setTransport } from "@/lib/api/client";
import { env } from "@/lib/env";
import { handlers, latency } from "./handlers";
import { timing } from "./simulator";

export async function startMocks() {
  timing.tickMs = env.mockTickMs;
  latency.ms = env.mockLatencyMs;
  if (env.mockTransport === "inline") {
    // No service worker (single-file build): run the handlers in-process.
    setTransport(
      async (request) => (await getResponse(handlers, request)) ?? new Response("Not mocked", { status: 501 }),
    );
    return;
  }
  const worker = setupWorker(...handlers);
  await worker.start({
    onUnhandledRequest: "bypass",
    quiet: true,
    serviceWorker: { url: `${import.meta.env.BASE_URL}mockServiceWorker.js` },
  });
}
