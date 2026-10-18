import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { hasFfmpeg } from "@/lib/features/ffmpeg";
import type { FeaturesResponse } from "@/lib/features/server";
import { createArrStack } from "@/lib/providers/media-manager/__fixtures__/env";
import { GET } from "./route";

vi.mock("@/lib/features/ffmpeg", () => ({ hasFfmpeg: vi.fn(async () => true) }));

describe("/api/features", () => {
  let stack: ReturnType<typeof createArrStack>;
  beforeEach(() => {
    stack = createArrStack();
    vi.stubEnv("TMDB_API_KEY", "");
    vi.mocked(hasFfmpeg).mockResolvedValue(true);
  });
  afterEach(() => stack.stop());

  async function features() {
    const text = await (await GET()).text();
    const body = JSON.parse(text) as FeaturesResponse;
    return { text, byId: Object.fromEntries(body.features.map((f) => [f.id, f])) };
  }

  it("only counts instances that are ready, and says what is missing", async () => {
    // A Radarr URL with no key is configured but not ready.
    vi.stubEnv("RADARR_URL", "http://radarr.example.com:7878");
    vi.stubEnv("TMDB_API_KEY", "tmdb-key-for-this-test");
    const { byId } = await features();
    expect(byId.library).toMatchObject({
      available: false,
      missing: ["a Sonarr or Radarr instance"],
    });
    expect(byId.explore).toMatchObject({ available: false, missing: ["a Radarr instance"] });
  });

  it("needs ffmpeg for Codecs", async () => {
    await stack.sonarr();
    vi.mocked(hasFfmpeg).mockResolvedValue(false);
    const { byId } = await features();
    expect(byId.library.available).toBe(true);
    expect(byId.codecs).toMatchObject({ available: false, missing: ["ffmpeg"] });
  });

  it("says nothing about hosts or keys", async () => {
    await stack.radarr();
    vi.stubEnv("TMDB_API_KEY", "tmdb-key-for-this-test");
    const { text, byId } = await features();
    expect(byId.explore.available).toBe(true);
    expect(text).not.toContain("127.0.0.1");
    expect(text).not.toContain("tmdb-key-for-this-test");
  });
});
