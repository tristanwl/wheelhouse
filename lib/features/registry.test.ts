import { describe, expect, it } from "vitest";
import {
  describeRequirement,
  getAvailableFeatures,
  type FeatureInstance,
  type FeatureId,
} from "./registry";

const radarr: FeatureInstance = { role: "mediaManager", kind: "movies", ready: true };
const sonarr: FeatureInstance = { role: "mediaManager", kind: "series", ready: true };
const tmdb: FeatureInstance = { role: "metadata", ready: true };
const sab: FeatureInstance = { role: "downloadClient", ready: true };
const broken = (i: FeatureInstance): FeatureInstance => ({ ...i, ready: false });

function available(instances: FeatureInstance[], ffmpeg = true): FeatureId[] {
  return getAvailableFeatures(instances, { ffmpeg })
    .filter((f) => f.available)
    .map((f) => f.feature.id);
}

describe("getAvailableFeatures", () => {
  const cases: [string, FeatureInstance[], boolean, FeatureId[]][] = [
    ["nothing configured", [], true, ["dashboard"]],
    ["only a download client", [sab], true, ["dashboard"]],
    ["only Sonarr", [sonarr], true, ["dashboard", "library", "codecs"]],
    ["only Radarr", [radarr], true, ["dashboard", "library", "codecs"]],
    ["TMDB alone", [tmdb], true, ["dashboard"]],
    ["Radarr and TMDB", [radarr, tmdb], true, ["dashboard", "library", "explore", "codecs"]],
    [
      "Sonarr and TMDB (Explore adds movies)",
      [sonarr, tmdb],
      true,
      ["dashboard", "library", "codecs"],
    ],
    ["Radarr without TMDB", [radarr, sab], true, ["dashboard", "library", "codecs"]],
    ["everything, no ffmpeg", [radarr, sonarr, tmdb], false, ["dashboard", "library", "explore"]],
    ["no manager, ffmpeg present", [tmdb], true, ["dashboard"]],
    ["a broken Radarr does not count", [broken(radarr), tmdb], true, ["dashboard"]],
    [
      "a broken TMDB does not count",
      [radarr, broken(tmdb)],
      true,
      ["dashboard", "library", "codecs"],
    ],
    [
      "a working Sonarr covers library when Radarr is broken",
      [broken(radarr), sonarr, tmdb],
      true,
      ["dashboard", "library", "codecs"],
    ],
  ];

  it.each(cases)("%s", (_name, instances, ffmpeg, expected) => {
    expect(available(instances, ffmpeg)).toEqual(expect.arrayContaining(expected));
    expect(available(instances, ffmpeg)).toHaveLength(expected.length);
  });

  it("lists what is missing, in words", () => {
    const result = getAvailableFeatures([], { ffmpeg: false });
    const reasons = Object.fromEntries(
      result.map((f) => [f.feature.id, f.missing.map(describeRequirement)])
    );
    expect(reasons).toEqual({
      dashboard: [],
      library: ["a Sonarr or Radarr instance"],
      explore: ["a TMDB API key", "a Radarr instance"],
      codecs: ["a Sonarr or Radarr instance", "ffmpeg"],
    });
  });

  it("reports only the unmet requirements", () => {
    const explore = getAvailableFeatures([radarr], { ffmpeg: true }).find(
      (f) => f.feature.id === "explore"
    )!;
    expect(explore.missing.map(describeRequirement)).toEqual(["a TMDB API key"]);
  });

  it("always has the dashboard, first", () => {
    const [first] = getAvailableFeatures([], { ffmpeg: false });
    expect(first.feature.id).toBe("dashboard");
    expect(first.available).toBe(true);
  });
});
