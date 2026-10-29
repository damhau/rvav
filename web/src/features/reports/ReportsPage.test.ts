import { describe, expect, it } from "vitest";
import { countTicks, seconds } from "./ReportsPage";

describe("countTicks", () => {
  it("gives whole steps of 1, 2 or 5 times a power of ten, the last at or above max", () => {
    expect(countTicks(0)).toEqual([0, 1]);
    expect(countTicks(3)).toEqual([0, 1, 2, 3]);
    expect(countTicks(7)).toEqual([0, 2, 4, 6, 8]);
    expect(countTicks(48)).toEqual([0, 10, 20, 30, 40, 50]);
    expect(countTicks(130)).toEqual([0, 50, 100, 150]);
  });
});

describe("seconds", () => {
  it("says durations as people do", () => {
    expect(seconds(undefined)).toBe("none");
    expect(seconds(45)).toBe("45 s");
    expect(seconds(600)).toBe("10 min");
    expect(seconds(7800)).toBe("2 h 10 min");
    expect(seconds(3 * 86400)).toBe("3 days");
  });
});
