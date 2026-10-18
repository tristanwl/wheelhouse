import { describe, expect, it } from "vitest";
import { formatEta } from "./format";

describe("formatEta", () => {
  it("formats as H:MM:SS", () => {
    expect(formatEta(432)).toBe("0:07:12");
    expect(formatEta(3723)).toBe("1:02:03");
  });

  it("keeps counting hours past a day", () => {
    expect(formatEta(49 * 3600 + 12 * 60 + 3)).toBe("49:12:03");
  });

  it("never goes negative", () => {
    expect(formatEta(-5)).toBe("0:00:00");
  });
});
