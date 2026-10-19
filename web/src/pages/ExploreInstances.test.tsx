import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ExploreHit } from "@/lib/types";
import { ALL_FEATURES, mockApi, renderApp } from "@/test-utils";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const hd = { instanceId: "radarr", name: "Radarr" };
const uhd = { instanceId: "radarr-4k", name: "Radarr 4K" };

const inHd: ExploreHit = {
  id: 603,
  alreadyAdded: true,
  libraryInstances: [hd],
  title: "Example Movie 1",
  year: 2024,
  overview: "",
  posterUrl: null,
  voteAverage: 7.5,
};
const inNeither: ExploreHit = {
  ...inHd,
  id: 604,
  alreadyAdded: false,
  libraryInstances: [],
  title: "Example Movie 2",
};

function meta(ref: { instanceId: string; name: string }, profile: string, instances = [hd, uhd]) {
  return {
    instanceId: ref.instanceId,
    instanceName: ref.name,
    qualityProfiles: [{ id: ref.instanceId === "radarr" ? 3 : 7, name: profile }],
    rootFolderPath: ref.instanceId === "radarr" ? "/movies" : "/movies-4k",
    instances,
  };
}

function stack(instances = [hd, uhd]) {
  return mockApi({
    "GET /api/features": ALL_FEATURES,
    "GET /api/add-meta?kind=movies": meta(hd, "HD - 720p/1080p", instances),
    "GET /api/add-meta?instanceId=radarr-4k": meta(uhd, "Ultra-HD", instances),
    "GET /api/explore": { results: [inHd, inNeither], hasMore: false },
    "POST /api/add": { ok: true },
  });
}

describe("Explore with several Radarr instances", () => {
  it("offers a picker, loads the chosen instance's profiles and adds there", async () => {
    const user = userEvent.setup();
    const { calls } = stack();
    renderApp("/explore");

    const picker = await screen.findByRole("combobox", { name: "Add to" });
    expect(
      within(picker)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["Radarr", "Radarr 4K"]);
    expect(picker).toHaveValue("radarr");

    await user.selectOptions(picker, "radarr-4k");
    expect(calls.some((c) => c.url === "/api/add-meta?instanceId=radarr-4k")).toBe(true);

    // The first movie is only in Radarr (HD), so it can be added to the 4K instance.
    const addButtons = await screen.findAllByRole("button", { name: "Add" });
    await vi.waitFor(() => expect(addButtons[0]).toBeEnabled());
    await user.click(addButtons[0]);
    const dialog = screen.getByText("Quality").closest(".fixed") as HTMLElement;
    expect(within(dialog).getByText("Adding to Radarr 4K")).toBeInTheDocument();
    expect(within(dialog).getByRole("combobox")).toHaveValue("7");
    await user.click(within(dialog).getByRole("button", { name: "Add" }));

    await vi.waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({
      instanceId: "radarr-4k",
      id: 603,
      qualityProfileId: 7,
      rootFolderPath: "/movies-4k",
    });
  });

  it("names the instances that have the movie, and still allows adding to the other", async () => {
    const user = userEvent.setup();
    stack();
    renderApp("/explore");

    // Target Radarr: the movie is there.
    expect((await screen.findAllByText("In library: Radarr")).length).toBeGreaterThan(0);
    // Switch to 4K: it is only in Radarr, so it can be added here and says where it is.
    await user.selectOptions(await screen.findByRole("combobox", { name: "Add to" }), "radarr-4k");
    expect((await screen.findAllByText("In Radarr")).length).toBeGreaterThan(0);
    expect(screen.queryAllByText("In library: Radarr")).toHaveLength(0);
  });

  it("shows no picker, and the plain badge, with one instance", async () => {
    stack([hd]);
    renderApp("/explore");

    expect((await screen.findAllByText("In library")).length).toBeGreaterThan(0);
    expect(screen.queryByRole("combobox", { name: "Add to" })).not.toBeInTheDocument();
  });

  it("offers the picker on a category page too", async () => {
    mockApi({
      "GET /api/features": ALL_FEATURES,
      "GET /api/add-meta?kind=movies": meta(hd, "HD - 720p/1080p"),
      "GET /api/explore?category=popular&limit=40&page=1": { results: [inNeither], hasMore: false },
    });
    renderApp("/explore/popular");
    expect(await screen.findByRole("combobox", { name: "Add to" })).toBeInTheDocument();
  });
});
