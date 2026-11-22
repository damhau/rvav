import { beforeEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { compositeGraph } from "./blueprint";
import type { CanvasGraph } from "./graph";

beforeEach(async () => {
  await i18n.changeLanguage("fr");
});

// What the canvas writes itself; the steps' titles, keys, groups and CEL are the author's.
const english = new Set(
  (
    "the then step steps fails goes escalates requester owner person people approve reject answer answers " +
    "event case otherwise other side of runs after first request when if and to done undo waits for tells says " +
    "asks wants timeout calls every until it"
  ).split(" "),
);

/** Every word the canvas wrote, but code (lines in monospace) and what the author named. */
function written(g: CanvasGraph): string[] {
  const texts = [
    ...g.nodes.flatMap((n) => [
      n.badge ?? "",
      ...n.lines.flatMap((l) => [l.label ?? "", l.mono ? "" : l.text]),
      ...n.relations,
      ...(n.sides ?? []).map((s) => s.label),
    ]),
    ...g.edges.flatMap((e) => [e.label ?? "", e.description]),
  ];
  return texts.flatMap((t) => t.toLowerCase().split(/[^a-zà-ÿ]+/u)).filter(Boolean);
}

describe("a composite on the canvas, in French", () => {
  const blueprint = {
    kind: "composite",
    steps: [
      {
        key: "accord",
        title: "Accord du chef",
        ask: { requester: true, owner: true, approvers: ["u1", "u2"], timeout: "48h", on_timeout: "continue" },
      },
      { key: "vote", title: "Vote", ask: { group: "dba", form: [{ key: "ok", label: "OK", type: "boolean" }] } },
      { key: "feu", title: "Feu vert", signal: { name: "feu-vert", timeout: "72h" } },
      {
        key: "pret",
        title: "Prêt",
        signal: { event: "provisioning.post", timeout: "1h", on_timeout: "continue" },
      },
      {
        key: "ticket",
        title: "Ticket",
        poll: {
          rest: { method: "GET", url: "https://itsm.example/x" },
          until: "steps.ticket.status == 200",
          every: "10m",
          timeout: "72h",
        },
      },
      {
        key: "voie",
        title: "Quelle voie",
        branch: {
          cases: [
            {
              title: "Refusé",
              when: "steps.vote.answered",
              steps: [{ key: "dire", title: "Le dire", notify: { requester: true, users: ["u1"], message: "Non" } }],
            },
            { when: "steps.ticket.status == 404", steps: [] },
          ],
          else: [{ key: "tous", title: "Tous", notify: { groups: ["dba"] } }],
        },
      },
      { key: "autres", title: "Prévenir", notify: {} },
    ],
  };

  it("words what the steps do in French, with nothing left in English", () => {
    const g = compositeGraph(blueprint);
    const left = written(g).filter((w) => english.has(w));
    expect(left).toEqual([]);
  });

  it("says whom a step asks or tells, what for and for how long", () => {
    const g = compositeGraph(blueprint);
    const lines = (id: string) => g.nodes.find((n) => n.id === id)!.lines.filter((l) => !l.mono);
    expect(lines("step:accord")).toEqual([
      { label: "Demande à", text: "le demandeur, le propriétaire, 2 personnes" },
      { label: "Attend", text: "une approbation ou un refus" },
      { label: "Délai", text: "48h, puis elle continue" },
    ]);
    expect(lines("step:vote")).toEqual([
      { label: "Demande à", text: "dba" },
      { label: "Attend", text: "1 réponse" },
    ]);
    expect(lines("step:feu")).toEqual([
      { label: "Attend", text: "le signal feu-vert" },
      { label: "Délai", text: "72h, puis l'étape échoue" },
    ]);
    expect(lines("step:pret")).toEqual([
      { label: "Attend", text: "l'événement provisioning.post" },
      { label: "Délai", text: "1h, puis elle continue" },
    ]);
    expect(lines("step:ticket")).toEqual([
      { label: "Toutes les", text: "10m" },
      { label: "Délai", text: "72h, puis l'étape échoue" },
    ]);
    expect(lines("step:dire")[0]).toEqual({ label: "Prévient", text: "le demandeur, 1 personne" });
    expect(lines("step:autres")).toEqual([{ label: "Prévient", text: "d'autres personnes" }]);
  });

  it("names a branch's sides as cases, and Otherwise as Sinon", () => {
    const g = compositeGraph(blueprint);
    const branch = g.nodes.find((n) => n.id === "step:voie")!;
    expect(branch.sides!.map((s) => s.label)).toEqual(["Refusé", "Cas 2", "Sinon"]);
    expect(g.nodes.find((n) => n.id === "step:tous")!.relations[0]).toBe("Dans le cas Sinon de Quelle voie");
    expect(g.edges.find((e) => e.source === "step:voie" && e.target === "step:autres")?.label).toBe("Cas 2");
  });

  it("switches back to English with the language", async () => {
    await i18n.changeLanguage("en");
    const g = compositeGraph(blueprint);
    expect(g.nodes.find((n) => n.id === "step:voie")!.sides!.map((s) => s.label)).toEqual([
      "Refusé",
      "Case 2",
      "Otherwise",
    ]);
    expect(g.nodes.find((n) => n.id === "step:feu")!.lines).toContainEqual({
      label: "Waits for",
      text: "the signal feu-vert",
    });
  });
});
