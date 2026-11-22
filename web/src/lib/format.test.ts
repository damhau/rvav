import { beforeEach, describe, expect, it } from "vitest";
import {
  clock,
  duration,
  formatDate,
  formatList,
  formatNumber,
  formatPercent,
  formatSize,
  fullDate,
  intlLocale,
  leaseLabel,
  longDay,
  relative,
  roughDuration,
} from "./format";
import { i18n } from "./i18n";

const at = "2027-03-05T14:30:05Z";
const now = new Date(at).getTime();
const ago = (s: number) => new Date(now - s * 1000).toISOString();
const days = (n: number) => new Date(Date.now() + (n - 0.5) * 86_400_000).toISOString();
const nbsp = "\u00a0";

describe("in English", () => {
  it("formats dates and times as en-GB", () => {
    expect(intlLocale()).toBe("en-GB");
    expect(fullDate(at)).toMatch(/^05 Mar 2027, \d\d:30$/);
    expect(longDay(at)).toMatch(/^\d March 2027$/);
    expect(clock(at)).toMatch(/^\d\d:30:05$/);
    expect(formatDate(at, { month: "long", year: "numeric", timeZone: "UTC" })).toBe("March 2027");
  });

  it("says how long ago, and how long", () => {
    expect(relative(ago(10), now)).toBe("just now");
    expect(relative(new Date(now + 10_000).toISOString(), now)).toBe("in a moment");
    expect(relative(ago(180), now)).toBe("3 minutes ago");
    expect(relative(ago(86_400), now)).toBe("yesterday");
    expect(duration(ago(45), at)).toBe("45s");
    expect(duration(ago(185), at)).toBe("3m 05s");
    expect(duration(ago(7380), at)).toBe("2h 3m");
    expect(roughDuration(60)).toBe("1 min");
    expect(roughDuration(420)).toBe("about 7 min");
  });

  it("says when a lease ends", () => {
    expect(leaseLabel(null)).toBe("No expiry");
    expect(leaseLabel(days(-1))).toBe("Expired");
    expect(leaseLabel(days(1))).toBe("Tomorrow");
    expect(leaseLabel(days(5))).toBe("In 5 days");
  });

  it("formats numbers, sizes and lists", () => {
    expect(formatNumber(1234.5)).toBe("1,234.5");
    expect(formatPercent(0.125)).toBe("12.5%");
    expect(formatSize(1234.5, "GB")).toBe("1,234.5 GB");
    expect(formatList(["a", "b", "c"])).toBe("a, b and c");
    expect(formatList(["a", "b"], "disjunction")).toBe("a or b");
  });
});

describe("in French", () => {
  // The setup puts English back after each test.
  beforeEach(() => i18n.changeLanguage("fr"));

  it("formats dates and times as French", () => {
    expect(intlLocale()).toBe("fr-FR");
    expect(fullDate(at)).toMatch(/^05 mars 2027(,| à) \d\d:30$/);
    expect(longDay(at)).toMatch(/^\d mars 2027$/);
    expect(clock(at)).toMatch(/^\d\d:30:05$/);
    expect(formatDate(at, { month: "long", year: "numeric", timeZone: "UTC" })).toBe("mars 2027");
  });

  it("says how long ago, and how long", () => {
    expect(relative(ago(10), now)).toBe("à l'instant");
    expect(relative(ago(180), now)).toBe("il y a 3 minutes");
    expect(relative(ago(86_400), now)).toBe("hier");
    expect(duration(ago(45), at)).toBe(`45${nbsp}s`);
    expect(duration(ago(185), at)).toBe(`3${nbsp}min 05${nbsp}s`);
    expect(duration(ago(7380), at)).toBe(`2${nbsp}h 3${nbsp}min`);
    expect(roughDuration(420)).toBe(`environ 7${nbsp}min`);
  });

  it("says when a lease ends, in the plural French needs", () => {
    expect(leaseLabel(null)).toBe("Sans échéance");
    expect(leaseLabel(days(1))).toBe("Demain");
    expect(leaseLabel(days(5))).toBe("Dans 5 jours");
  });

  it("formats numbers, sizes and lists", () => {
    // ICU versions differ on which no-break space they put.
    expect(formatNumber(1234.5)).toMatch(/^1[\u00a0\u202f]234,5$/);
    expect(formatPercent(0.125)).toMatch(/^12,5[\u00a0\u202f]%$/);
    expect(formatSize(1234.5, "GB")).toMatch(/^1[\u00a0\u202f]234,5\u00a0Go$/);
    expect(formatList(["a", "b", "c"])).toBe("a, b et c");
    expect(formatList(["a", "b"], "disjunction")).toBe("a ou b");
  });
});
