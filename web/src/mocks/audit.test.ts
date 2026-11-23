// The mock's audit log names an event's target as the server does
// (internal/store/audit_targets.go).
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { apiUrl, rawFetch } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { audited } from "./governance";
import { db, resetDb } from "./seed";

type AuditEvent = components["schemas"]["AuditEvent"];

afterEach(() => resetDb());

// Vitest runs in web/, where its configuration is.
const serverTypes = [
  ...readFileSync(resolve(process.cwd(), "../internal/store/audit_targets.go"), "utf8").matchAll(
    /^\t\{"([a-z0-9_]+)", "select /gm,
  ),
].map((m) => m[1]!);

const events = async () => {
  const r = await rawFetch(new Request(apiUrl("/audit")));
  return ((await r.json()) as { items: AuditEvent[] }).items;
};

describe("the mock's audit log", () => {
  it("knows each target type the server names, and says when one is gone", async () => {
    expect(serverTypes).toContain("size_preset");
    for (const type of serverTypes) audited(db, "test.gone", type, "gone");
    const gone = (await events()).filter((e) => e.action === "test.gone");
    expect(gone.filter((e) => e.target_exists !== false).map((e) => e.target_type)).toEqual([]);
    expect(gone).toHaveLength(serverTypes.length);
  });

  it("names a record deleted with no event before, and the seeded approval and lease", async () => {
    const r = await rawFetch(new Request(apiUrl("/admin/sizes/M"), { method: "DELETE" }));
    expect(r.status).toBe(204);
    const all = await events();
    expect(all.find((e) => e.action === "size.delete")).toMatchObject({
      target_type: "size_preset",
      target_id: "M",
      target_name: "Medium",
      target_exists: false,
    });
    expect(all.find((e) => e.action === "approval.timeout")).toMatchObject({
      target_name: "REQ-10477 · Infrastructure leads",
      target_exists: true,
    });
    expect(all.find((e) => e.action === "policy.lease.set")).toMatchObject({
      target_name: "Development",
      target_exists: true,
    });
  });
});
