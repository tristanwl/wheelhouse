import { describe, expect, it } from "vitest";
import {
  MAX_DOWNLOAD_ROWS,
  capDownloads,
  describeDownloadRow,
  downloadTone,
  sortDownloads,
} from "./download-rows";
import type { DownloadItem, DownloadState } from "./providers/download-client/types";

function item(over: Partial<DownloadItem> = {}): DownloadItem {
  return {
    id: "1",
    name: "Example",
    protocol: "torrent",
    state: "downloading",
    progress: 0.5,
    sizeBytes: 2048,
    remainingBytes: 1024,
    etaSeconds: null,
    ...over,
  };
}

describe("sortDownloads", () => {
  it("puts failures, then active work, then waiting, then seeding", () => {
    const order: DownloadState[] = [
      "seeding",
      "queued",
      "completed",
      "paused",
      "downloading",
      "stalled",
      "postprocessing",
      "failed",
    ];
    expect(sortDownloads(order.map((state) => ({ state }))).map((i) => i.state)).toEqual([
      "failed",
      "postprocessing",
      "downloading",
      "stalled",
      "queued",
      "paused",
      "seeding",
      "completed",
    ]);
  });

  it("keeps the incoming order within a state, and doesn't mutate its input", () => {
    const input = [
      { id: "a", state: "seeding" as const },
      { id: "b", state: "downloading" as const },
      { id: "c", state: "seeding" as const },
      { id: "d", state: "downloading" as const },
    ];
    expect(sortDownloads(input).map((i) => i.id)).toEqual(["b", "d", "a", "c"]);
    expect(input.map((i) => i.id)).toEqual(["a", "b", "c", "d"]);
  });
});

describe("capDownloads", () => {
  it("shows everything under the cap", () => {
    expect(capDownloads([1, 2, 3])).toEqual({ shown: [1, 2, 3], hidden: 0 });
  });

  it("caps at 50 by default and counts the rest", () => {
    const rows = Array.from({ length: 73 }, (_, i) => i);
    const { shown, hidden } = capDownloads(rows);
    expect(MAX_DOWNLOAD_ROWS).toBe(50);
    expect(shown).toHaveLength(50);
    expect(shown[0]).toBe(0);
    expect(hidden).toBe(23);
  });

  it("is exact at the boundary", () => {
    expect(capDownloads([1, 2, 3], 3).hidden).toBe(0);
    expect(capDownloads([1, 2, 3, 4], 3)).toEqual({ shown: [1, 2, 3], hidden: 1 });
  });
});

describe("downloadTone", () => {
  it("flags failures, calls out stalls and checks, and calms seeding", () => {
    expect(downloadTone("failed")).toBe("bad");
    expect(downloadTone("stalled")).toBe("warn");
    expect(downloadTone("postprocessing")).toBe("warn");
    expect(downloadTone("seeding")).toBe("good");
    expect(downloadTone("downloading")).toBe("neutral");
    expect(downloadTone("queued")).toBe("neutral");
  });
});

describe("describeDownloadRow", () => {
  it("shows progress, the client's speed and the ETA for a download", () => {
    expect(describeDownloadRow(item({ etaSeconds: 65 }), 2048)).toEqual({
      left: "50% of 2.0 KB",
      right: "2.0 KB/s · 0:01:05 left",
    });
  });

  it("leaves the speed off rows that aren't downloading", () => {
    expect(describeDownloadRow(item({ state: "queued" }), 2048).right).toBe("");
    expect(describeDownloadRow(item({ state: "stalled" }), 2048).right).toBe("");
  });

  it("shows ratio and upload speed for a seeding row", () => {
    const seeding = item({
      state: "seeding",
      progress: 1,
      torrent: { ratio: 1.5, uploadBytesPerSec: 1024 },
    });
    expect(describeDownloadRow(seeding)).toEqual({
      left: "2.0 KB",
      right: "ratio 1.50 · ↑ 1.0 KB/s",
    });
    const idle = item({ state: "seeding", torrent: { ratio: 0.2, uploadBytesPerSec: 0 } });
    expect(describeDownloadRow(idle).right).toBe("ratio 0.20");
  });

  it("shows only the size while a check or move runs", () => {
    expect(describeDownloadRow(item({ state: "postprocessing", stateDetail: "Checking" }))).toEqual(
      { left: "2.0 KB", right: "" }
    );
  });

  it("copes with a magnet that has no size yet", () => {
    expect(describeDownloadRow(item({ sizeBytes: 0, progress: 0 })).left).toBe("0%");
  });
});
