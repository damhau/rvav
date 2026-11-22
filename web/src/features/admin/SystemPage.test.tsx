import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { screen, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api, call } from "@/lib/api/client";
import { server } from "@/mocks/node";
import { resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

// The System page (U1.5, ADR-0030), read only, against the mock API.
afterEach(() => {
  vi.useRealTimers();
  localStorage.removeItem("rvav.mock.fresh");
});

describe("System page", () => {
  it("lists the install settings the server does, in its order", async () => {
    // Vitest runs in web/, where its configuration is.
    const go = readFileSync(resolve(process.cwd(), "../internal/config/install.go"), "utf8");
    const names = [...go.matchAll(/^\t\{"(RVAV_[A-Z0-9_]+)"/gm)].map((m) => m[1]);
    const system = await call(api.GET("/admin/system"));
    expect(system.install.map((s) => s.name)).toEqual(names);
  });

  it("shows the version, the processes, the settings saved and the install settings, secrets hidden", async () => {
    renderApp("/admin/system");
    expect(await screen.findByRole("heading", { name: "System", level: 1 })).toBeInTheDocument();
    const install = await screen.findByRole("region", { name: "This install" });
    expect(install).toHaveTextContent("Version1.4.0");
    expect(install).toHaveTextContent("Migration 49");
    expect(install).toHaveTextContent("FIPS 140-3 modeOnly: other algorithms refusedGo Cryptographic Module v1.0.0");

    const procs = within(screen.getByRole("table", { name: "Processes" })).getAllByRole("row");
    expect(procs).toHaveLength(5);
    expect(procs[1]).toHaveTextContent(/API.*rvav-api-7d9f-2k4xq.*1\.4\.0.*Only.*Running/);
    expect(screen.queryByText(/FIPS 140-3 modes/)).toBeNull();

    const saved = screen.getByRole("table", { name: "Settings saved in Administration" });
    expect(within(saved).getByRole("row", { name: /vCenter/ })).toHaveTextContent(/Applied/);

    const settings = screen.getByRole("table", { name: "Install settings" });
    const row = (name: string) => within(settings).getByRole("row", { name: new RegExp(`^${name} `) });
    expect(row("RVAV_DATABASE_URL")).toHaveTextContent(/rvav:xxxxx@rvav-db.*password hidden.*Environment/);
    expect(row("RVAV_SECRETS_KEY")).toHaveTextContent(/^RVAV_SECRETS_KEYSetEnvironment$/);
    expect(row("RVAV_ADMIN_PASSWORD")).toHaveTextContent(/Not setDefault$/);
    expect(row("RVAV_TEMPORAL_ADDRESS")).toHaveTextContent("temporal-frontend.rvav.svc:7233");
    expect(row("RVAV_CORS_ORIGINS")).toHaveTextContent(/noneDefault$/);
    // Nothing on it changes anything.
    expect(screen.getByRole("main").querySelectorAll("button, input, select, textarea")).toHaveLength(0);
  });

  it("says how far each saved setting has applied", async () => {
    // The mock's processes start again some time after a save: not while
    // the clock stands still.
    vi.useFakeTimers({ toFake: ["Date"] });
    const list = await call(api.GET("/admin/integrations"));
    const vcenter = list.items.find((it) => it.kind === "vcenter")!;
    const { url: _url, ...rest } = vcenter.settings;
    await call(
      api.PUT("/admin/integrations/{kind}", {
        params: { path: { kind: "vcenter" } },
        body: { settings: { ...rest, name: "vc02.corp.example" } },
      }),
    );
    const smtp = list.items.find((it) => it.kind === "smtp")!;
    await call(
      api.PUT("/admin/integrations/{kind}", {
        params: { path: { kind: "smtp" } },
        body: { settings: { ...smtp.settings, host: "smtp2.corp.example" } },
      }),
    );
    renderApp("/admin/system");
    const saved = await screen.findByRole("table", { name: "Settings saved in Administration" });
    const rows = within(saved).getAllByRole("row");
    // The latest first: email is read on next use, vCenter's waits for the processes.
    expect(rows[1]).toHaveTextContent(/^Email3.*On next use$/);
    expect(rows[2]).toHaveTextContent(/^vCenter2.*by .*Applying$/);
    expect(screen.getByRole("region", { name: "This install" })).toHaveTextContent("Revision 2");
    expect(within(screen.getByRole("table", { name: "Processes" })).getAllByText("earlier settings")).toHaveLength(4);
  });

  it("links each saved setting to its tab, the console's to vCenter's", async () => {
    const list = await call(api.GET("/admin/integrations"));
    const console = list.items.find((it) => it.kind === "console")!;
    await call(
      api.PUT("/admin/integrations/{kind}", {
        params: { path: { kind: "console" } },
        body: { settings: { ...console.settings, username: "svc-console@vsphere.local" } },
      }),
    );
    renderApp("/admin/system");
    const saved = await screen.findByRole("table", { name: "Settings saved in Administration" });
    expect(within(saved).getByRole("link", { name: "Console" })).toHaveAttribute("href", "/admin/integrations/vcenter");
    expect(within(saved).getByRole("link", { name: "vCenter" })).toHaveAttribute("href", "/admin/integrations/vcenter");
  });

  it("says when processes run in different FIPS 140-3 modes", async () => {
    const system = await call(api.GET("/admin/system"));
    const [first, ...others] = system.processes;
    server.use(
      http.get("*/api/v1/admin/system", () =>
        HttpResponse.json({
          ...system,
          fips: { mode: "off", module: "latest" },
          processes: [{ ...first!, fips: { mode: "off", module: "latest" } }, ...others],
        }),
      ),
    );
    renderApp("/admin/system");
    const install = await screen.findByRole("region", { name: "This install" });
    expect(install).toHaveTextContent("FIPS 140-3 modeOff: no FIPS 140 modeGo's module, not a validated one");
    const processes = screen.getByRole("region", { name: "Processes" });
    expect(within(processes).getByRole("status")).toHaveTextContent(
      "Processes run in 2 FIPS 140-3 modes: give each the same RVAV_FIPS, then start them again.",
    );
  });

  it("leaves out the module of a process too old to report it", async () => {
    const system = await call(api.GET("/admin/system"));
    const [first, ...others] = system.processes;
    server.use(
      http.get("*/api/v1/admin/system", () =>
        HttpResponse.json({ ...system, processes: [{ ...first!, fips: { mode: "off", module: "" } }, ...others] }),
      ),
    );
    renderApp("/admin/system");
    const processes = await screen.findByRole("region", { name: "Processes" });
    const row = await within(processes).findByRole("row", { name: new RegExp(first!.host) });
    expect(within(row).getByText("Off")).not.toHaveAttribute("title");
    expect(within(processes).getAllByTitle(/^Go Cryptographic Module /)).toHaveLength(others.length);
  });

  it("on a new install, nothing is saved in Administration yet", async () => {
    localStorage.setItem("rvav.mock.fresh", "1");
    resetDb();
    renderApp("/admin/system");
    expect(await screen.findByText(/None yet: every setting comes from the environment/)).toBeInTheDocument();
    expect(within(screen.getByRole("main")).getByRole("link", { name: "Integrations" })).toHaveAttribute(
      "href",
      "/admin/integrations",
    );
  });
});
