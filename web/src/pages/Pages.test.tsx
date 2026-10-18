import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { youtubeWatchUrl } from "@/lib/api";
import type { DownloadsResponse, ExploreDetail, ExploreHit, StatusResponse } from "@/lib/types";
import { ALL_FEATURES, mockApi, renderApp } from "@/test-utils";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const status: StatusResponse = {
  instances: [
    {
      id: "radarr",
      providerId: "radarr",
      name: "Radarr",
      role: "mediaManager",
      state: "ok",
      link: { protocol: "http", port: 7878, path: "/" },
      detail: {
        version: "5.0.0",
        health: [],
        queueCount: 2,
        indexers: { total: 3, enabled: 2, issues: [] },
      },
    },
    {
      id: "sabnzbd",
      providerId: "sabnzbd",
      name: "SABnzbd",
      role: "downloadClient",
      state: "down",
      error: "connection refused",
    },
  ],
};

const downloads: DownloadsResponse = {
  instances: [
    { instanceId: "sabnzbd", name: "SABnzbd", up: true, paused: true, speedBytesPerSec: 0 },
  ],
  items: [
    {
      instanceId: "sabnzbd",
      id: "1",
      name: "Example.Download.1",
      protocol: "usenet",
      state: "downloading",
      progress: 0.5,
      sizeBytes: 2 * 1024 ** 3,
      remainingBytes: 1024 ** 3,
      etaSeconds: 60,
    },
  ],
};

describe("Dashboard", () => {
  it("shows a card per instance, storage bars that link to the library, and downloads", async () => {
    mockApi({
      "GET /api/features": ALL_FEATURES,
      "GET /api/status": status,
      "GET /api/storage": {
        entries: [
          {
            instanceId: "radarr",
            label: "Movies",
            path: "/movies",
            freeBytes: 100 * 1024 ** 3,
            libraryUsedBytes: 50 * 1024 ** 3,
          },
        ],
        instances: [],
      },
      "GET /api/downloads": downloads,
    });
    renderApp("/");

    const radarr = (await screen.findByRole("heading", { name: "Radarr" })).closest("a")!;
    // Links use the host the browser used to reach Wheelhouse, and open in a new tab.
    expect(radarr).toHaveAttribute("href", "http://localhost:7878/");
    expect(radarr).toHaveAttribute("target", "_blank");
    expect(radarr).toHaveAttribute("rel", "noopener noreferrer");
    expect(within(radarr).getByText("queue: 2")).toBeInTheDocument();
    expect(within(radarr).getByText("indexers: 2/3")).toBeInTheDocument();

    const sab = screen.getByRole("heading", { name: "SABnzbd" }).closest("div")!.parentElement!;
    expect(within(sab).getByText("connection refused")).toBeInTheDocument();

    expect(await screen.findByText("100.0 GB free")).toBeInTheDocument();
    expect(screen.getByText("100.0 GB free").closest("a")).toHaveAttribute(
      "href",
      "/library?path=%2Fmovies"
    );

    expect(await screen.findByText("Example.Download.1")).toBeInTheDocument();
    expect(screen.getByText("Queue paused")).toBeInTheDocument();
    expect(screen.getByText("50% of 2.0 GB")).toBeInTheDocument();
  });

  it("polls status every 15 s, storage every 30 s and downloads every 5 s", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { calls } = mockApi({
      "GET /api/features": ALL_FEATURES,
      "GET /api/status": status,
      "GET /api/storage": { entries: [], instances: [] },
      "GET /api/downloads": downloads,
    });
    renderApp("/");
    await screen.findByText("Example.Download.1");
    const count = (path: string) => calls.filter((c) => c.url === path).length;
    const before = {
      status: count("/api/status"),
      storage: count("/api/storage"),
      downloads: count("/api/downloads"),
    };
    expect(before).toEqual({ status: 1, storage: 1, downloads: 1 });

    await act(() => vi.advanceTimersByTimeAsync(5100));
    expect(count("/api/downloads")).toBe(2);
    expect(count("/api/status")).toBe(1);

    await act(() => vi.advanceTimersByTimeAsync(10_000)); // 15.1 s
    expect(count("/api/status")).toBe(2);
    expect(count("/api/downloads")).toBe(4);
    expect(count("/api/storage")).toBe(1);

    await act(() => vi.advanceTimersByTimeAsync(15_000)); // 30.1 s
    expect(count("/api/storage")).toBe(2);
  });
});

const hit: ExploreHit = {
  id: 603,
  alreadyAdded: false,
  title: "Example Movie 1",
  year: 2024,
  overview: "An example.",
  posterUrl: "https://image.tmdb.org/t/p/w342/example.jpg",
  voteAverage: 7.5,
};

const detail: ExploreDetail = {
  ...hit,
  genres: ["Drama"],
  runtime: 125,
  backdropUrl: "https://image.tmdb.org/t/p/w780/backdrop.jpg",
  trailerKey: "abc123XYZ_-",
};

function exploreStack(extra: Record<string, unknown> = {}) {
  return mockApi({
    "GET /api/features": ALL_FEATURES,
    "GET /api/add-meta": {
      instanceId: "radarr",
      instanceName: "Radarr",
      qualityProfiles: [
        { id: 4, name: "Ultra-HD" },
        { id: 3, name: "HD - 720p/1080p" },
      ],
      rootFolderPath: "/movies",
    },
    "GET /api/explore": { results: [hit], hasMore: false },
    "GET /api/explore/603": detail,
    "POST /api/add": { ok: true },
    ...extra,
  });
}

describe("Explore", () => {
  it("adds a movie with the chosen options, defaulting to the HD profile", async () => {
    const user = userEvent.setup();
    const { calls } = exploreStack();
    renderApp("/explore");

    const addButtons = await screen.findAllByRole("button", { name: "Add" });
    await waitForEnabled(addButtons[0]);
    await user.click(screen.getAllByRole("checkbox", { name: "Search now" }).at(-1)!); // the page's, not the global search box's
    await user.click(addButtons[0]);

    const dialog = screen.getByText("Quality").closest(".fixed") as HTMLElement;
    expect(within(dialog).getByRole("combobox")).toHaveValue("3");
    await user.click(within(dialog).getByRole("button", { name: "Add" }));

    expect(await screen.findAllByText("Added ✓")).not.toHaveLength(0);
    const post = calls.find((c) => c.method === "POST");
    expect(post?.url).toBe("/api/add");
    expect(post?.body).toEqual({
      instanceId: "radarr",
      id: 603,
      qualityProfileId: 3,
      rootFolderPath: "/movies",
      monitored: true,
      searchNow: false,
      minimumAvailability: "released",
    });
  });

  it("shows the server's error when an add fails, and keeps the dialog open", async () => {
    const user = userEvent.setup();
    exploreStack({ "POST /api/add": { status: 502, body: { error: "Radarr is down" } } });
    renderApp("/explore");

    const addButtons = await screen.findAllByRole("button", { name: "Add" });
    await waitForEnabled(addButtons[0]);
    await user.click(addButtons[0]);
    const dialog = screen.getByText("Quality").closest(".fixed") as HTMLElement;
    await user.click(within(dialog).getByRole("button", { name: "Add" }));

    expect(await within(dialog).findByText("Radarr is down")).toBeInTheDocument();
  });

  it("opens a detail modal whose trailer is a link out, not an embedded frame", async () => {
    const user = userEvent.setup();
    exploreStack();
    const { container } = renderApp("/explore");

    await user.click((await screen.findAllByRole("button", { name: /Example Movie 1/ }))[0]);
    const trailer = await screen.findByRole("link", { name: /Watch Trailer/ });
    expect(trailer).toHaveAttribute("href", "https://www.youtube.com/watch?v=abc123XYZ_-");
    expect(trailer).toHaveAttribute("target", "_blank");
    expect(trailer).toHaveAttribute("rel", "noopener noreferrer");
    expect(container.querySelector("iframe")).toBeNull();
    expect(screen.getByText(/2h 5m/)).toBeInTheDocument();
  });

  it("youtubeWatchUrl accepts a YouTube key and nothing else", () => {
    expect(youtubeWatchUrl("abc123XYZ_-")).toBe("https://www.youtube.com/watch?v=abc123XYZ_-");
    expect(youtubeWatchUrl(null)).toBeNull();
    expect(youtubeWatchUrl("")).toBeNull();
    expect(youtubeWatchUrl("x&list=evil")).toBeNull();
    expect(youtubeWatchUrl("../../etc")).toBeNull();
  });
});

async function waitForEnabled(button: HTMLElement) {
  await vi.waitFor(() => expect(button).toBeEnabled());
}
