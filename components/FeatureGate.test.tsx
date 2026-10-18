import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { FeaturesResponse } from "@/lib/features/server";
import { FeatureStub } from "./FeatureGate";
import { NavLinks } from "./FeatureNav";

const features: FeaturesResponse["features"] = [
  { id: "dashboard", label: "Dashboard", route: "/", available: true, missing: [] },
  { id: "library", label: "Library", route: "/library", available: true, missing: [] },
  {
    id: "explore",
    label: "Explore",
    route: "/explore",
    available: false,
    missing: ["a TMDB API key"],
  },
  { id: "codecs", label: "Codecs", route: "/codecs", available: false, missing: ["ffmpeg"] },
];

describe("NavLinks", () => {
  it("shows only the available features", () => {
    const html = renderToStaticMarkup(<NavLinks features={features} />);
    expect(html).toContain("Dashboard");
    expect(html).toContain("Library");
    expect(html).not.toContain("Explore");
    expect(html).not.toContain("Codecs");
  });

  it("shows just the Dashboard until the list loads", () => {
    const html = renderToStaticMarkup(<NavLinks features={undefined} />);
    expect(html).toContain("Dashboard");
    expect(html).not.toContain("Library");
  });
});

describe("FeatureStub", () => {
  it("says what is missing and links to the README configuration section", () => {
    const html = renderToStaticMarkup(
      <FeatureStub label="Explore" missing={["a TMDB API key", "a Radarr instance"]} />
    );
    expect(html).toContain("Explore");
    expect(html).toContain("a TMDB API key and a Radarr instance");
    expect(html).toContain("https://github.com/tristanwl/wheelhouse#configuration");
  });
});
