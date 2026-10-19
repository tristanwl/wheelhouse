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
  hdr: "none" as const,
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
  hdrExcluded: 0,
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
  hdr: "none",
  owner: "user-1",
  ownerName: "example-user",
  acknowledgedHardlink: false,
  acknowledgedHdr: false,
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

  it("says how many HDR files are left out, and shows who started a job", async () => {
    codecStack({ "GET /api/codec-analysis": { ...analysis, hdrExcluded: 2 } });
    renderApp("/codecs");
    expect(await screen.findByText(/2 HDR or Dolby Vision files are left out/)).toBeInTheDocument();
  });

  describe("HDR and Dolby Vision", () => {
    const hdrAnalysis = {
      ...analysis,
      candidates: [{ ...analysis.candidates[0], hdr: "dolby-vision" as const }],
    };

    it("marks HDR candidates with a badge", async () => {
      codecStack({ "GET /api/codec-analysis": hdrAnalysis });
      renderApp("/codecs");
      expect(await screen.findByText("Dolby Vision")).toBeInTheDocument();
    });

    it("needs the acknowledgement ticked before it converts, and sends it", async () => {
      const user = userEvent.setup();
      const { calls } = codecStack({
        "GET /api/codec-analysis": hdrAnalysis,
        "POST /api/codec-analysis/convert": { ...job, hdr: "dolby-vision", acknowledgedHdr: true },
      });
      const dialog = await openConvert(user);
      expect(within(dialog).getByRole("alert")).toHaveTextContent("This file is Dolby Vision");
      const go = within(dialog).getByRole("button", { name: "Convert anyway" });
      expect(go).toBeDisabled();
      await user.click(go);
      expect(calls.some((c) => c.method === "POST")).toBe(false);

      await user.click(within(dialog).getByRole("checkbox", { name: /SDR 8-bit/ }));
      expect(go).toBeEnabled();
      await user.click(go);
      expect(await within(dialog).findByText("Converting… 40%")).toBeInTheDocument();
      const post = calls.find((c) => c.method === "POST");
      expect(post?.body).toHaveProperty("acknowledgeHdr", true);
    });

    it("asks when the server finds HDR in a file the analysis called SDR", async () => {
      const user = userEvent.setup();
      let attempt = 0;
      const { calls } = codecStack({
        "POST /api/codec-analysis/convert": () => {
          attempt += 1;
          return attempt === 1
            ? { status: 409, body: { error: "hdr", code: "hdr", hdr: "hdr10" } }
            : { ...job, hdr: "hdr10", acknowledgedHdr: true };
        },
      });
      const dialog = await openConvert(user);
      await user.click(within(dialog).getByRole("button", { name: "Convert" }));
      expect(await within(dialog).findByRole("alert")).toHaveTextContent("This file is HDR10");
      await user.click(within(dialog).getByRole("checkbox", { name: /SDR 8-bit/ }));
      await user.click(within(dialog).getByRole("button", { name: "Convert anyway" }));
      expect(await within(dialog).findByText("Converting… 40%")).toBeInTheDocument();
      const posts = calls.filter((c) => c.method === "POST");
      expect(posts[0].body).not.toHaveProperty("acknowledgeHdr");
      expect(posts[1].body).toHaveProperty("acknowledgeHdr", true);
    });

    it("shows the refusal when HDR conversion is not allowed", async () => {
      const user = userEvent.setup();
      codecStack({
        "POST /api/codec-analysis/convert": {
          status: 409,
          body: {
            error: "HDR files are left out unless an administrator allows it",
            code: "hdr-excluded",
            hdr: "hlg",
          },
        },
      });
      const dialog = await openConvert(user);
      await user.click(within(dialog).getByRole("button", { name: "Convert" }));
      expect(
        await within(dialog).findByText(/left out unless an administrator/)
      ).toBeInTheDocument();
    });
  });
});
