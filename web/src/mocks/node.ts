// Mock API for Vitest.
import { setupServer } from "msw/node";
import { bus } from "./events";
import { faults } from "./faults";
import { handlers, latency } from "./handlers";
import { resetDb } from "./seed";
import { resetSignIn } from "./signIn";
import { stopAll, timing } from "./simulator";

export const server = setupServer(...handlers);

export function resetMocks() {
  stopAll();
  bus.reset();
  resetDb();
  resetSignIn();
  faults.reset();
  latency.ms = 0;
  timing.tickMs = 20;
}
