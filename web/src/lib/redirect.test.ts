import { describe, expect, it } from "vitest";
import { safeRedirect } from "./redirect";

describe("safeRedirect", () => {
  it("keeps paths inside the app", () => {
    expect(safeRedirect("/library?x=1#top")).toBe("/library?x=1#top");
    expect(safeRedirect("/a/./b/../c")).toBe("/a/c");
    expect(safeRedirect("/path%2F%2Fwith-encoded-slashes")).toBe("/path%2F%2Fwith-encoded-slashes");
  });

  it("rejects protocol-relative results of path normalisation", () => {
    for (const raw of [
      "/.//evil.example/x",
      "/./\\/evil.example",
      "/a/..//evil.example/x",
      "/./../..//evil.example",
      "//evil.example/x",
      "/\\evil.example",
      "/\\/evil.example",
      "/\t/evil.example",
      "/\n/evil.example",
      "/\r/evil.example",
      "/\u0000/evil.example",
      "/ok\u007f",
      "/.\t//evil.example",
    ]) {
      expect(safeRedirect(raw), raw).toBe("/");
    }
  });

  it("does not turn encoded variants into another site", () => {
    // Encoded slashes and backslashes stay encoded, so they are plain path text.
    for (const raw of [
      "/%2F/evil.example",
      "/%5Cevil.example",
      "/.%2F/evil.example",
      "/%2e//evil.example",
    ]) {
      const out = safeRedirect(raw);
      expect(out.startsWith("//"), raw).toBe(false);
      expect(out.includes("\\"), raw).toBe(false);
    }
  });

  it("refuses absolute URLs, other schemes, login and setup", () => {
    for (const raw of [
      "https://evil.example",
      "javascript:alert(1)",
      "login",
      "",
      null,
      undefined,
      "/login",
      "/setup/x",
      "/./login",
    ]) {
      expect(safeRedirect(raw)).toBe("/");
    }
    expect(safeRedirect("/nope", "/home")).toBe("/nope");
    expect(safeRedirect("//x", "/home")).toBe("/home");
  });
});
