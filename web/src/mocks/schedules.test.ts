// The mocks' schedules against the server's (ADR-0049): a cron expression
// may name its days and months as Temporal reads them, a name too long has
// its own code, and a new schedule's owner was just seen.
import { describe, expect, it } from "vitest";
import { api, call, isApiError } from "@/lib/api/client";
import type { ScheduleWrite } from "@/lib/api/types";

async function write(change: Partial<ScheduleWrite>): Promise<ScheduleWrite> {
  const tls = await call(api.GET("/schedules/{scheduleId}", { params: { path: { scheduleId: "sch_tlsweekly" } } }));
  return {
    name: "Workdays' TLS renewal",
    catalog_item_id: tls.catalog_item.id,
    version_id: tls.version.id,
    project_id: tls.project.id,
    environment: tls.environment,
    inputs: tls.inputs,
    cron: "0 6 * * 1",
    timezone: "UTC",
    ...change,
  };
}

describe("the mocks' schedules", () => {
  it("read the names of days and months, and say who owns one was just seen", async () => {
    const s = await call(api.POST("/schedules", { body: await write({ cron: "30 7 * JAN-DEC MON-FRI" }) }));
    expect(s.next_runs).toHaveLength(5);
    for (const at of s.next_runs ?? []) expect([1, 2, 3, 4, 5]).toContain(new Date(at).getUTCDay());
    expect(Date.now() - new Date(s.owner_seen_at!).getTime()).toBeLessThan(60_000);
  });

  it("find a run beyond the next two months, as Temporal does", async () => {
    const s = await call(api.POST("/schedules", { body: await write({ cron: "0 6 29 2 *" }) }));
    expect(s.next_runs?.length).toBeGreaterThan(0);
    for (const at of s.next_runs ?? []) expect(at).toMatch(/^\d{4}-02-29T06:00:00/);
  });

  it("refuse a name too long with its own code", async () => {
    try {
      await call(api.POST("/schedules", { body: await write({ name: "x".repeat(121) }) }));
      expect.unreachable();
    } catch (e) {
      if (!isApiError(e)) throw e;
      expect(e.fieldErrors.filter((x) => x.field === "/name")).toEqual([
        { field: "/name", code: "too_long", message: "Name it in 120 characters at most." },
      ]);
    }
  });
});
