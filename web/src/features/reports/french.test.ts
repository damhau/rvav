import { beforeEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { seconds } from "./ReportsPage";

beforeEach(async () => {
  await i18n.changeLanguage("fr");
});

describe("seconds, in French", () => {
  it("says durations with a no-break space before the unit, and days in French", () => {
    expect(seconds(undefined)).toBe("aucun");
    expect(seconds(45)).toBe("45\u00a0s");
    expect(seconds(7800)).toBe("2\u00a0h 10\u00a0min");
    expect(seconds(3 * 86400)).toBe("3\u00a0jours");
  });
});
