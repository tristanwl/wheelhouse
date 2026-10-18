import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createArrStack } from "@/lib/providers/media-manager/__fixtures__/env";
import { fixtureMovies, fourKMovies } from "@/lib/providers/media-manager/__fixtures__/server";
import { GET, type CodecAnalysisResponse } from "./route";

describe("/api/codec-analysis", () => {
  let stack: ReturnType<typeof createArrStack>;
  beforeEach(() => {
    stack = createArrStack();
  });
  afterEach(() => stack.stop());

  async function analyze(kind: string) {
    const res = await GET(new Request(`http://localhost/api/codec-analysis?kind=${kind}`));
    return {
      status: res.status,
      body: (await res.json()) as CodecAnalysisResponse & { error?: string },
    };
  }

  const filesOf = (body: CodecAnalysisResponse) => [...body.candidates, ...body.matching];

  it("analyzes the movies of every Radarr instance and tags files with theirs", async () => {
    await stack.radarr();
    await stack.radarr4k({ movies: fourKMovies() });
    const { body } = await analyze("movies");

    expect(body.totalCount).toBe(
      fixtureMovies().filter((m) => m.movieFile?.mediaInfo?.videoCodec).length + 2
    );
    expect(new Set(filesOf(body).map((f) => f.instanceId))).toEqual(
      new Set(["radarr", "radarr-4k"])
    );
    // The movie without a file is skipped, not guessed at.
    expect(body.skippedCount).toBe(fixtureMovies().filter((m) => !m.movieFile).length);
  });

  it("analyzes series across Sonarr, one request per series for files and episodes", async () => {
    const sonarr = await stack.sonarr();
    await stack.radarr();
    const { body } = await analyze("series");

    expect(body.totalCount).toBe(2);
    expect(new Set(filesOf(body).map((f) => f.instanceId))).toEqual(new Set(["sonarr"]));
    expect(sonarr.count("GET", "/api/v3/episodefile")).toBe(3);
    expect(sonarr.count("GET", "/api/v3/episode")).toBe(3);
  });

  it("keeps going with the instances that work, and lists the ones that don't", async () => {
    await stack.radarr();
    await stack.radarr4k({ movies: fourKMovies() }, "wrong-key-value-123");
    const { status, body } = await analyze("movies");
    expect(status).toBe(200);
    expect(body.problems).toEqual([
      { instanceId: "radarr-4k", name: "Radarr 4K", error: "radarr-4k: API key incorrect" },
    ]);
    expect(filesOf(body).some((f) => f.instanceId === "radarr-4k")).toBe(false);
  });

  it("says plainly when there is nothing to analyze", async () => {
    await stack.sonarr();
    const { status, body } = await analyze("movies");
    expect(status).toBe(404);
    expect(body.error).toBe("No Radarr instance is configured");
  });
});
