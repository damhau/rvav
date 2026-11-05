import { describe, expect, it } from "vitest";
import { compositeGraph, vmGraph } from "./blueprint";
import { FRAME, layoutGraph, nearest, nodeHeight, type Box } from "./layout";

const composite = compositeGraph({
  kind: "composite",
  steps: [
    { key: "vm", item: "rhel9-server" },
    {
      parallel: [
        { key: "db_user", awx: { job_template: "pg-create-role" }, undo: { job_template: "pg-drop-role" } },
        { key: "monitoring", workflow: "register-monitoring" },
      ],
    },
    { key: "notice", awx: { job_template: "dba-notice" }, when: "inputs.notify" },
  ],
});

const overlap = (a: Box, b: Box) =>
  a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;

describe("layoutGraph", () => {
  it("lays a composite out top to bottom, a parallel group's steps side by side in their frame", () => {
    const l = layoutGraph(composite);
    const y = (id: string) => l.boxes.get(id)!.y;
    expect(y("inputs")).toBeLessThan(y("step:vm"));
    expect(y("step:vm")).toBeLessThan(y("step:db_user"));
    expect(y("step:db_user")).toBe(y("step:monitoring"));
    expect(y("step:monitoring")).toBeLessThan(y("step:notice"));
    expect(y("step:notice")).toBeLessThan(y("end"));

    const frame = l.groups.get("parallel:1")!;
    const db = l.nodes.get("step:db_user")!;
    const mon = l.nodes.get("step:monitoring")!;
    expect(db).toMatchObject({ parent: "parallel:1", x: FRAME.pad, y: FRAME.label + FRAME.pad });
    expect(mon.x).toBe(db.x + db.width + FRAME.gap);
    // The frame holds both, and its steps' boxes are on the canvas too.
    expect(frame.width).toBe(2 * FRAME.pad + db.width + FRAME.gap + mon.width);
    expect(frame.height).toBe(FRAME.label + 2 * FRAME.pad + Math.max(db.height, mon.height));
    expect(l.boxes.get("step:db_user")).toEqual({
      x: frame.x + db.x,
      y: frame.y + db.y,
      width: db.width,
      height: db.height,
    });

    const top = ["inputs", "step:vm", "step:notice", "end"].map((id) => l.nodes.get(id)!);
    for (const a of [...top, frame]) for (const b of [...top, frame]) if (a !== b) expect(overlap(a, b)).toBe(false);
    expect(l.width).toBeGreaterThanOrEqual(frame.width);
  });

  it("sizes a node by what it shows", () => {
    const vm = composite.nodes.find((n) => n.id === "step:vm")!;
    const db = composite.nodes.find((n) => n.id === "step:db_user")!;
    expect(nodeHeight(vm)).toBe(18 + 20 + 16); // its target
    expect(nodeHeight(db)).toBe(18 + 20 + 16 * 2); // its target and undo
    expect(layoutGraph(composite).nodes.get("step:db_user")?.height).toBe(nodeHeight(db));
  });

  it("lays a VM item out in columns: networks, components, then groups", () => {
    const g = vmGraph({
      kind: "vm",
      segments: [{ key: "app" }],
      bindings: { network: "segments.app" },
      security_groups: [
        { key: "web", rules: [{ from: ["any"], services: ["HTTPS"] }] },
        { key: "app", rules: [{ from: ["web"], services: ["TCP-8080"] }] },
        { key: "db", rules: [{ from: ["app"], services: ["MSSQL"] }] },
      ],
      components: [
        { key: "web", groups: ["web"] },
        { key: "app", groups: ["app"] },
        { key: "db", groups: ["db"] },
      ],
    });
    const l = layoutGraph(g);
    const x = (id: string) => l.boxes.get(id)!.x;
    expect(x("segment:app")).toBeLessThan(x("component:web"));
    expect(new Set(["component:web", "component:app", "component:db"].map(x)).size).toBe(1);
    expect(x("component:web")).toBeLessThan(x("sg:web"));
    expect(x("any")).toBeLessThan(x("sg:web"));
    // Rules between the groups don't push one a column further: they are drawn beside them.
    expect(new Set(["sg:web", "sg:app", "sg:db"].map(x)).size).toBe(1);
    expect([...l.side]).toEqual(["rule:app:0:0", "rule:db:0:0"]);
  });
});

describe("nearest", () => {
  const l = layoutGraph(composite);
  it("moves the way of the arrow, to the nearest node in line", () => {
    expect(nearest(l.boxes, "inputs", "down")).toBe("step:vm");
    // The next layer first: a parallel group's steps before the step under them.
    expect(["step:db_user", "step:monitoring"]).toContain(nearest(l.boxes, "step:vm", "down"));
    expect(nearest(l.boxes, "step:db_user", "right")).toBe("step:monitoring");
    expect(nearest(l.boxes, "step:monitoring", "left")).toBe("step:db_user");
    expect(nearest(l.boxes, "step:db_user", "down")).toBe("step:notice");
    expect(nearest(l.boxes, "end", "up")).toBe("step:notice");
  });

  it("stays when nothing is that way", () => {
    expect(nearest(l.boxes, "inputs", "up")).toBeUndefined();
    expect(nearest(l.boxes, "end", "down")).toBeUndefined();
    expect(nearest(l.boxes, "nowhere", "down")).toBeUndefined();
  });
});
