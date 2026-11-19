import { describe, expect, it } from "vitest";
import type { UiField } from "@/lib/api/types";
import { computedBy, defaulted, sentInputs, singleSource, withDefaults } from "./defaults";

const cel = { kind: "cel", params: { value: "1" } } as const;

describe("defaults", () => {
  it("tells a field a source gives one value from one it gives options", () => {
    expect(singleSource({ widget: "number", label: "Cores", value_source: cel })).toBe(true);
    expect(singleSource({ widget: "computed", label: "Tag", value_source: cel })).toBe(true);
    expect(singleSource({ widget: "select", label: "Owner", value_source: cel })).toBe(false);
    expect(singleSource({ widget: "text", label: "Token", secret: true, value_source: cel })).toBe(false);
    expect(singleSource({ widget: "text", label: "Zone", value_source: { kind: "builtin", name: "ipam.next" } })).toBe(
      false,
    );
  });

  it("keeps worked-out values apart from defaults", () => {
    const rule: UiField = { widget: "text", label: "Host", rules: { default: "inputs.app" } };
    expect(defaulted(rule)).toBe(true);
    expect(computedBy({ ...rule, rules: { value: "inputs.app" } })).toBe(true);
    expect(defaulted({ ...rule, rules: { value: "inputs.app" } })).toBe(false);
    expect(computedBy({ widget: "computed", label: "Tag", value_source: cel })).toBe(true);
    expect(defaulted({ widget: "number", label: "Cores", value_source: cel })).toBe(true);
    expect(defaulted({ widget: "text", label: "Name" })).toBe(false);
  });

  it("shows a default only while the field has no value of the requester's", () => {
    const states = {
      host: { visible: true, required: false, read_only: false, default: "web" },
      fqdn: { visible: true, required: false, read_only: false, default: "web.corp" },
      zone: { visible: true, required: false, read_only: false },
    };
    const values = { app: "web", fqdn: undefined };
    expect(withDefaults(values, states)).toEqual({ app: "web", host: "web", fqdn: undefined });
    expect(withDefaults(values)).toBe(values);
  });

  it("shows a read-only field's default, else its initial value, whatever the requester typed", () => {
    const ro = { visible: true, required: false, read_only: true };
    const states = { schema: { ...ro, default: "fixed" }, tier: ro, note: ro };
    expect(withDefaults({ schema: "mine", tier: "gold", note: "typed" }, states, { tier: "bronze" })).toEqual({
      schema: "fixed",
      tier: "bronze",
    });
    const kept = { schema: "fixed" };
    expect(withDefaults(kept, { schema: states.schema })).toBe(kept);
  });

  it("sends a defaulted field the requester emptied as null, and leaves the others out", () => {
    const fields: Record<string, UiField> = {
      host: { widget: "text", label: "Host", rules: { default: "inputs.app" } },
      note: { widget: "text", label: "Note" },
    };
    expect(sentInputs(fields, { app: "web", host: undefined, note: undefined })).toEqual({
      app: "web",
      host: null,
      note: undefined,
    });
    const untouched = { app: "web" };
    expect(sentInputs(fields, untouched)).toBe(untouched);
  });
});
