import { screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ALL_FEATURES, mockApi, renderApp } from "./test-utils";
import type { FeaturesResponse } from "./lib/types";

afterEach(() => vi.unstubAllGlobals());

const emptyStack = {
  "GET /api/features": ALL_FEATURES,
  "GET /api/status": { instances: [] },
  "GET /api/storage": { entries: [], instances: [] },
  "GET /api/downloads": { instances: [], items: [] },
  "GET /api/library": { items: [], instances: [] },
  "GET /api/add-meta": {
    instanceId: "radarr",
    instanceName: "Radarr",
    qualityProfiles: [{ id: 1, name: "HD - 720p/1080p" }],
    rootFolderPath: "/movies",
  },
  "GET /api/explore": { results: [], hasMore: false },
  "GET /api/codec-analysis": {
    totalCount: 0,
    totalSize: 0,
    preferences: { hd: "h264", uhd: "hevc" },
    buckets: {
      h264: { count: 0, totalSize: 0, percentOfLibrary: 0 },
      hevc: { count: 0, totalSize: 0, percentOfLibrary: 0 },
      legacy: { count: 0, totalSize: 0, percentOfLibrary: 0 },
    },
    candidates: [],
    matching: [],
    skippedCount: 0,
  },
  "GET /api/codec-analysis/settings": { hd: "h264", uhd: "hevc" },
  "GET /api/codec-analysis/convert": { jobs: [] },
};

function nav() {
  return within(screen.getByRole("navigation"));
}

describe("routes", () => {
  it("renders the Dashboard at /", async () => {
    mockApi(emptyStack);
    renderApp("/");
    expect(await screen.findByRole("heading", { name: "Service status" })).toBeInTheDocument();
    expect(await screen.findByText(/No services are configured yet/)).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Storage" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Downloads" })).toBeInTheDocument();
  });

  it("renders the Library at /library, filtered by ?path=", async () => {
    mockApi({
      ...emptyStack,
      "GET /api/library": {
        instances: [{ instanceId: "radarr", name: "Radarr", kind: "movies", up: true }],
        items: [
          item({ id: 1, title: "Example Movie 1", rootFolderPath: "/movies" }),
          item({ id: 2, title: "Example Movie 2", rootFolderPath: "/movies-4k" }),
        ],
      },
    });
    renderApp("/library?path=%2Fmovies-4k");
    expect(await screen.findByText("Example Movie 2")).toBeInTheDocument();
    expect(screen.queryByText("Example Movie 1")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Library (1)" })).toBeInTheDocument();
  });

  it("renders Explore at /explore and a category at /explore/:category", async () => {
    mockApi(emptyStack);
    const first = renderApp("/explore");
    expect(await screen.findByRole("heading", { name: "In Theaters" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Popular" })).toBeInTheDocument();
    first.unmount();

    renderApp("/explore/popular");
    expect(await screen.findByRole("heading", { name: "Popular" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "‹ Explore" })).toHaveAttribute("href", "/explore");
  });

  it("says so for an unknown Explore category", async () => {
    mockApi(emptyStack);
    renderApp("/explore/nonsense");
    expect(await screen.findByText(/Unknown category/)).toBeInTheDocument();
  });

  it("renders Codecs at /codecs", async () => {
    mockApi(emptyStack);
    renderApp("/codecs");
    expect(await screen.findByRole("heading", { name: "Codec Analysis" })).toBeInTheDocument();
    expect(await screen.findByText(/Nothing worth converting/)).toBeInTheDocument();
  });

  it("shows a 404 page, inside the frame, for any other path", async () => {
    mockApi(emptyStack);
    renderApp("/no/such/page");
    expect(await screen.findByRole("heading", { name: "Page not found" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "wheelhouse home" })).toBeInTheDocument();
  });
});

describe("feature gating", () => {
  const partial: FeaturesResponse = {
    features: [
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
    ],
  };

  it("shows only the available features in the nav", async () => {
    mockApi({ ...emptyStack, "GET /api/features": partial });
    renderApp("/");
    await waitFor(() => expect(nav().getByRole("link", { name: "Library" })).toBeInTheDocument());
    expect(nav().getByRole("link", { name: "Dashboard" })).toHaveAttribute("href", "/");
    expect(nav().queryByRole("link", { name: "Explore" })).not.toBeInTheDocument();
    expect(nav().queryByRole("link", { name: "Codecs" })).not.toBeInTheDocument();
  });

  it("shows just the Dashboard link until the list loads", async () => {
    mockApi({ ...emptyStack, "GET /api/features": () => new Promise(() => {}) });
    renderApp("/");
    await screen.findByRole("navigation");
    expect(nav().getByRole("link", { name: "Dashboard" })).toBeInTheDocument();
    expect(nav().queryByRole("link", { name: "Library" })).not.toBeInTheDocument();
  });

  it("replaces an unavailable page with the stub that says what is missing", async () => {
    const { calls } = mockApi({ ...emptyStack, "GET /api/features": partial });
    renderApp("/explore");
    expect(await screen.findByText("Explore isn't available yet")).toBeInTheDocument();
    expect(screen.getByText(/a TMDB API key/)).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /Configuration section of the README/ })
    ).toHaveAttribute("href", "https://github.com/tristanwl/wheelhouse#configuration");
    // The gated page never ran, so it asked the server for nothing.
    expect(calls.some((c) => c.url.startsWith("/api/explore"))).toBe(false);
  });

  it("stubs Codecs on a direct visit, naming ffmpeg", async () => {
    mockApi({ ...emptyStack, "GET /api/features": partial });
    renderApp("/codecs");
    expect(await screen.findByText("Codecs isn't available yet")).toBeInTheDocument();
    expect(screen.getByText(/It needs ffmpeg/)).toBeInTheDocument();
  });

  it("hides the global add/search box when Library is unavailable", async () => {
    const noLibrary: FeaturesResponse = {
      features: partial.features.map((f) => (f.id === "library" ? { ...f, available: false } : f)),
    };
    mockApi({ ...emptyStack, "GET /api/features": noLibrary });
    renderApp("/");
    await screen.findByText(/No services are configured yet/);
    expect(screen.queryByPlaceholderText(/search/i)).not.toBeInTheDocument();
  });

  it("lets the page through when the feature list cannot be loaded", async () => {
    mockApi({ ...emptyStack, "GET /api/features": { status: 500, body: { error: "boom" } } });
    renderApp("/codecs");
    expect(await screen.findByRole("heading", { name: "Codec Analysis" })).toBeInTheDocument();
  });
});

function item(over: Partial<import("./lib/types").LibraryItem>): import("./lib/types").LibraryItem {
  return {
    instanceId: "radarr",
    instanceName: "Radarr",
    kind: "movies",
    id: 1,
    title: "Example Movie 1",
    sizeOnDisk: 1024 ** 3,
    monitored: true,
    rootFolderPath: "/movies",
    added: "2025-01-01T00:00:00Z",
    ...over,
  };
}
