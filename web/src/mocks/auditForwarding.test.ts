// The mock's audit destinations against the server's checks
// (internal/core/audit_forwarding.go, decoded strictly).
import { afterEach, describe, expect, it } from "vitest";
import { apiUrl, rawFetch } from "@/lib/api/client";
import { resetDb } from "./seed";

afterEach(() => {
  localStorage.removeItem("rvav.mock.as");
  resetDb();
});

const send = async (path: string, init?: RequestInit) => {
  const r = await rawFetch(new Request(apiUrl(path), init));
  const body = (await r.json()) as { errors?: { field: string; code: string }[]; detail?: string };
  return { status: r.status, body };
};
const create = (body: unknown) => send("/admin/audit-destinations", { method: "POST", body: JSON.stringify(body) });
const syslog = (more: object) => create({ name: "Lab", syslog: { host: "siem.corp.example", ...more } });
const fields = async (body: unknown) => (await create(body)).body.errors?.map((e) => `${e.field} ${e.code}`);

describe("the mock's audit destinations", () => {
  it("refuse a field the server doesn't take, at any depth", async () => {
    expect((await create({ name: "Lab", syslog: { host: "a.example" }, colour: "red" })).body.detail).toBe(
      'json: unknown field "colour"',
    );
    expect((await syslog({ protocol: "tls" })).body.detail).toBe('json: unknown field "protocol"');
  });

  it("take a host name or an IP address, IPv6 too, as the server does", async () => {
    expect((await create({ name: "v6", syslog: { host: "2001:db8::10" } })).status).toBe(201);
    expect((await create({ name: "v4", syslog: { host: "10.0.0.7" } })).status).toBe(201);
    expect(await fields({ name: "Lab", syslog: { host: "siem..corp.example" } })).toEqual(["/syslog/host invalid"]);
    expect(await fields({ name: "Lab", syslog: { host: "-siem.example" } })).toEqual(["/syslog/host invalid"]);
    expect(await fields({ name: "Lab", syslog: { host: "fe80::1%eth0" } })).toEqual(["/syslog/host invalid"]);
  });

  it("refuse a transport that isn't one", async () => {
    expect(await fields({ name: "Lab", syslog: { host: "siem.corp.example", transport: "sctp" } })).toEqual([
      "/syslog/transport enum",
    ]);
  });

  it("count a name's characters, not its bytes", async () => {
    expect((await create({ name: "é".repeat(80), syslog: { host: "a.example" } })).status).toBe(201);
    expect(await fields({ name: "é".repeat(81), syslog: { host: "b.example" } })).toEqual(["/name required"]);
  });

  it("refuse a webhook's credential in its URL, and a header rvav sets itself", async () => {
    const hook = (more: object) => ({ name: "Hook", webhook: { url: "https://hooks.corp.example/x", secret: "webhook-signing", ...more } }); // prettier-ignore
    expect(await fields(hook({ url: "https://u:p@hooks.corp.example/x" }))).toEqual(["/webhook/url credentials"]);
    expect(await fields(hook({ auth_header: "Content-Type", auth_value: "x" }))).toEqual(["/webhook/auth_header reserved"]); // prettier-ignore
    expect(await fields(hook({ auth_value: "Bearer x" }))).toEqual(["/webhook/auth_header required"]);
    expect(await fields(hook({ auth_header: "Authorization" }))).toEqual(["/webhook/auth_value required"]);
    const made = await create(hook({ url: "https://hooks.corp.example/x?token=s3cret", auth_header: "Authorization", auth_value: "Bearer x" })); // prettier-ignore
    expect(made.status).toBe(201);
    expect(made.body).toMatchObject({
      webhook: { url: "https://hooks.corp.example/x?token=REDACTED", auth_header: "Authorization" },
    });
    expect(made.body).not.toHaveProperty("webhook.auth_value");
  });

  it("show a destination to those who may read the audit log only", async () => {
    expect((await send("/admin/audit-destinations/adt_siem")).status).toBe(200);
    localStorage.setItem("rvav.mock.as", "member");
    resetDb();
    expect((await send("/admin/audit-destinations/adt_siem")).status).toBe(403);
  });
});
