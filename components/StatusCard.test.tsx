import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { StatusCard } from "./StatusCard";

const NOW = Date.parse("2026-01-10T12:00:00Z");
const ago = (hours: number) => new Date(NOW - hours * 3_600_000).toISOString();
const LONG_PATH =
  "Saving /config/Downloads/incomplete/Example.Movie.2024.1080p.WEB-DL.x264-GROUP/__ADMIN__/SABnzbd_nzo_aaa111 failed";

function render(props: Partial<Parameters<typeof StatusCard>[0]>) {
  return renderToStaticMarkup(
    <StatusCard name="SABnzbd" state="ok" lines={[]} now={NOW} {...props} />
  );
}

describe("StatusCard", () => {
  it("wraps long unbroken text in warnings, lines and errors", () => {
    const html = render({
      lines: [LONG_PATH],
      warnings: [{ message: LONG_PATH, severity: "error", at: ago(1) }],
    });
    const wrapping = html.match(/break-words \[overflow-wrap:anywhere\]/g) ?? [];
    expect(wrapping.length).toBeGreaterThanOrEqual(3); // title, line, warning
    expect(html).toContain("min-w-0");

    const down = render({ state: "down", error: LONG_PATH });
    expect(down).toMatch(/text-red-400 min-w-0 break-words \[overflow-wrap:anywhere\]/);
  });

  it("shows age and severity, styled apart", () => {
    const html = render({
      warnings: [
        { message: "disk trouble", severity: "error", at: ago(1) },
        { message: "slow server", severity: "warning", at: ago(5) },
      ],
    });
    expect(html).toContain("1h ago");
    expect(html).toContain("5h ago");
    expect(html).toMatch(/text-red-400"[^>]*title="disk trouble"/);
    expect(html).toMatch(/text-amber-400"[^>]*title="slow server"/);
    expect(html).toContain(">Error<");
    expect(html).toContain(">Warning<");
  });

  it("drops warnings older than 24 hours but keeps undated ones", () => {
    const html = render({
      warnings: [
        { message: "ancient news", severity: "error", at: ago(25) },
        { message: "undated notice", severity: "warning" },
      ],
    });
    expect(html).not.toContain("ancient news");
    expect(html).toContain("undated notice");
    expect(html).not.toContain(" · ");
  });

  it("omits the warnings block when all are stale", () => {
    const html = render({ warnings: [{ message: "old", severity: "error", at: ago(48) }] });
    expect(html).not.toContain('<ul class="mt-2');
  });

  it("shows the error, not the lines, when down", () => {
    const html = render({
      state: "down",
      lines: ["queue: 1"],
      error: "radarr: connection refused",
    });
    expect(html).toContain("radarr: connection refused");
    expect(html).not.toContain("queue: 1");
    expect(html).toContain("bg-red-500");
  });
});
