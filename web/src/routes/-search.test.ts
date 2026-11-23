import { describe, expect, it } from "vitest";
import { optDay } from "./-search";

describe("optDay", () => {
  it("keeps a day a date box can hold", () => {
    expect(optDay("2026-11-23")).toBe("2026-11-23");
    expect(optDay("2028-02-29")).toBe("2028-02-29");
    expect(optDay("0001-01-01")).toBe("0001-01-01");
  });

  it("drops a day that isn't one, though Date.parse takes it", () => {
    expect(optDay("2026-02-31")).toBeUndefined();
    expect(optDay("2026-02-29")).toBeUndefined();
    expect(optDay("2026-13-01")).toBeUndefined();
    expect(optDay("0000-01-01")).toBeUndefined();
    expect(optDay("2026-11-23T10:00")).toBeUndefined();
    expect(optDay(20261123)).toBeUndefined();
  });
});
