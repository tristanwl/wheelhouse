import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CodecAnalysisResponse, ConversionJob } from "@/lib/types";
import { ALL_FEATURES, mockApi, renderApp } from "@/test-utils";

afterEach(() => vi.unstubAllGlobals());

const file = {
  title: "Example Movie 1",
  codec: "xvid",
  resolution: "1920x1080",
  bitDepth: 8,
  size: 3 * 1024 ** 3,
  instanceId: "radarr",
  itemId: 7,
  filePath: "/movies/Example Movie 1 (2020)/Example Movie 1.avi",
};

const analysis: CodecAnalysisResponse = {
  totalCount: 1,
  totalSize: file.size,
  preferences: { hd: "h264", uhd: "hevc" },
  buckets: {
    h264: { count: 0, totalSize: 0, percentOfLibrary: 0 },
    hevc: { count: 0, totalSize: 0, percentOfLibrary: 0 },
    legacy: { count: 1, totalSize: file.size, percentOfLibrary: 100 },
  },
  candidates: [{ ...file, tier: "hd", targetFamily: "h264", reason: "legacy-codec" }],
  matching: [],
  skippedCount: 0,
};

const job: ConversionJob = {
  id: "job-1",
  instanceId: "radarr",
  itemId: 7,
  filePath: file.filePath,
  title: file.title,
  originalSize: file.size,
  backend: "software",
  targetFamily: "h264",
  status: "running",
  progressPercent: 40,
  log: "frame=1",
  createdAt: 1,
};

function codecStack(extra: Record<string, unknown> = {}) {
  return mockApi({
    "GET /api/features": ALL_FEATURES,
    "GET /api/codec-analysis": analysis,
    "GET /api/codec-analysis/settings": { hd: "h264", uhd: "hevc" },
    "GET /api/codec-analysis/encoders": {
      encoders: [
        { backend: "software", family: "h264", label: "libx264", available: true },
        { backend: "vaapi", family: "h264", label: "Intel", available: false, reason: "no device" },
      ],
    },
    "GET /api/codec-analysis/convert": { jobs: [] },
    "GET /api/codec-analysis/convert/job-1": job,
    "POST /api/codec-analysis/convert/job-1/cancel": { ...job, status: "canceled" },
    ...extra,
  });
}

async function openConvert(user: ReturnType<typeof userEvent.setup>) {
  renderApp("/codecs");
  await user.click(await screen.findByRole("button", { name: "Convert" }));
  return screen.getByText(/Re-encode this file/).closest(".fixed") as HTMLElement;
}

describe("Codecs", () => {
  it("lists candidates with the reason, and the buckets", async () => {
    codecStack();
    renderApp("/codecs");
    expect(await screen.findByText("Legacy codec → H.264")).toBeInTheDocument();
    expect(screen.getByText("Conversion candidates (1)")).toBeInTheDocument();
  });

  it("saves a changed preference with PUT", async () => {
    const user = userEvent.setup();
    const { calls } = codecStack({
      "PUT /api/codec-analysis/settings": { hd: "hevc", uhd: "hevc" },
    });
    renderApp("/codecs");
    const select = (await screen.findByText("HD")).closest("label")!.querySelector("select")!;
    await vi.waitFor(() => expect(select).toBeEnabled());
    await user.selectOptions(select, "hevc");
    const put = calls.find((c) => c.method === "PUT");
    expect(put?.url).toBe("/api/codec-analysis/settings");
    expect(put?.body).toEqual({ hd: "hevc", uhd: "hevc" });
  });

  it("starts a conversion with the chosen encoder and shows its progress", async () => {
    const user = userEvent.setup();
    const { calls } = codecStack({ "POST /api/codec-analysis/convert": job });
    const dialog = await openConvert(user);

    await user.click(within(dialog).getByRole("button", { name: "Convert" }));

    expect(await within(dialog).findByText("Converting… 40%")).toBeInTheDocument();
    const post = calls.find((c) => c.method === "POST");
    expect(post?.body).toEqual({
      instanceId: "radarr",
      itemId: 7,
      filePath: file.filePath,
      title: "Example Movie 1",
      originalSize: file.size,
      backend: "software",
      targetFamily: "h264",
    });

    await user.click(within(dialog).getByRole("button", { name: "Stop" }));
    expect(calls.some((c) => c.url === "/api/codec-analysis/convert/job-1/cancel")).toBe(true);
  });

  it("asks before converting a hard-linked file, then sends the acknowledgement", async () => {
    const user = userEvent.setup();
    let attempt = 0;
    const { calls } = codecStack({
      "POST /api/codec-analysis/convert": () => {
        attempt += 1;
        return attempt === 1
          ? { status: 409, body: { error: "hard links", code: "hardlinked", linkCount: 2 } }
          : job;
      },
    });
    const dialog = await openConvert(user);

    await user.click(within(dialog).getByRole("button", { name: "Convert" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "This file is also stored somewhere else"
    );
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);

    await user.click(within(dialog).getByRole("button", { name: "Convert anyway" }));
    expect(await within(dialog).findByText("Converting… 40%")).toBeInTheDocument();
    const posts = calls.filter((c) => c.method === "POST");
    expect(posts[0].body).not.toHaveProperty("acknowledgeHardlink");
    expect(posts[1].body).toHaveProperty("acknowledgeHardlink", true);
  });

  it("shows a refusal from the server", async () => {
    const user = userEvent.setup();
    codecStack({
      "POST /api/codec-analysis/convert": {
        status: 400,
        body: { error: "path is outside the media folders" },
      },
    });
    const dialog = await openConvert(user);
    await user.click(within(dialog).getByRole("button", { name: "Convert" }));
    expect(
      await within(dialog).findByText("path is outside the media folders")
    ).toBeInTheDocument();
  });
});
