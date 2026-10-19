import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { confirmedDeleteUrl } from "@/lib/api";
import type { LibraryItem, LibraryResponse } from "@/lib/types";
import { ALL_FEATURES, mockApi, renderApp, type Call } from "@/test-utils";

afterEach(() => vi.unstubAllGlobals());

const movie: LibraryItem = {
  instanceId: "radarr",
  instanceName: "Radarr",
  kind: "movies",
  id: 1,
  title: "Example Movie 1",
  sizeOnDisk: 2 * 1024 ** 3,
  monitored: true,
  rootFolderPath: "/movies",
  added: "2025-01-01T00:00:00Z",
};
const show: LibraryItem = {
  ...movie,
  instanceId: "sonarr",
  instanceName: "Sonarr",
  kind: "series",
  id: 5,
  title: "Example Show 1",
  rootFolderPath: "/tv",
};

const instances = [
  { instanceId: "radarr", name: "Radarr", kind: "movies" as const, up: true },
  { instanceId: "sonarr", name: "Sonarr", kind: "series" as const, up: true },
];

/** A library that forgets what was deleted, as the real one does. */
function libraryStack(extra: Record<string, unknown> = {}) {
  let items = [movie, show];
  const library = (): LibraryResponse => ({ items, instances });
  return mockApi({
    "GET /api/features": ALL_FEATURES,
    "GET /api/library": library,
    "GET /api/storage": { entries: [], instances: [] },
    "GET /api/add-meta": {
      instanceId: "radarr",
      instanceName: "Radarr",
      qualityProfiles: [],
      rootFolderPath: "/movies",
    },
    "DELETE /api/library/radarr/1?confirm=true": () => {
      items = items.filter((i) => i.id !== 1);
      return { ok: true };
    },
    "GET /api/library/sonarr/5/seasons": {
      title: "Example Show 1",
      seasons: [
        {
          seasonNumber: 1,
          monitored: true,
          episodeFileCount: 8,
          episodeCount: 8,
          totalEpisodeCount: 8,
          sizeOnDisk: 4 * 1024 ** 3,
        },
        {
          seasonNumber: 2,
          monitored: true,
          episodeFileCount: 0,
          episodeCount: 0,
          totalEpisodeCount: 8,
          sizeOnDisk: 0,
        },
      ],
    },
    "DELETE /api/library/sonarr/5/seasons/1?confirm=true": { ok: true },
    "DELETE /api/library/sonarr/5?confirm=true": () => {
      items = items.filter((i) => i.id !== 5);
      return { ok: true };
    },
    ...extra,
  });
}

const deletes = (calls: Call[]) => calls.filter((c) => c.method === "DELETE");

/** The confirm dialog with this title: the only way to tell its Delete button from the page's. */
const dialog = (title: string) => within(screen.getByText(title).closest(".fixed") as HTMLElement);

describe("deleting a movie", () => {
  it("sends nothing until the user confirms, then sends confirm=true", async () => {
    const user = userEvent.setup();
    const { calls } = libraryStack();
    renderApp("/library");

    const row = (await screen.findByText("Example Movie 1")).closest("tr")!;
    await user.click(within(row).getByRole("button", { name: "Delete" }));

    // The confirm dialog is up and no request has gone out.
    expect(screen.getByText('Delete "Example Movie 1"?')).toBeInTheDocument();
    expect(screen.getByText(/deletes 2\.0 GB of files from disk/)).toBeInTheDocument();
    expect(deletes(calls)).toEqual([]);

    await user.click(dialog('Delete "Example Movie 1"?').getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(screen.queryByText("Example Movie 1")).not.toBeInTheDocument());
    expect(deletes(calls).map((c) => c.url)).toEqual(["/api/library/radarr/1?confirm=true"]);
    expect(screen.queryByText('Delete "Example Movie 1"?')).not.toBeInTheDocument();
  });

  it("cancel closes the dialog and deletes nothing", async () => {
    const user = userEvent.setup();
    const { calls } = libraryStack();
    renderApp("/library");

    const row = (await screen.findByText("Example Movie 1")).closest("tr")!;
    await user.click(within(row).getByRole("button", { name: "Delete" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    expect(screen.queryByText('Delete "Example Movie 1"?')).not.toBeInTheDocument();
    expect(screen.getByText("Example Movie 1")).toBeInTheDocument();
    expect(deletes(calls)).toEqual([]);
  });

  it("shows the server's refusal and keeps the item", async () => {
    const user = userEvent.setup();
    libraryStack({
      "DELETE /api/library/radarr/1?confirm=true": {
        status: 502,
        body: { error: "Radarr: API key incorrect" },
      },
    });
    renderApp("/library");

    const row = (await screen.findByText("Example Movie 1")).closest("tr")!;
    await user.click(within(row).getByRole("button", { name: "Delete" }));
    await user.click(dialog('Delete "Example Movie 1"?').getByRole("button", { name: "Delete" }));

    expect(await screen.findByText(/Radarr: API key incorrect/)).toBeInTheDocument();
    expect(screen.getByText("Example Movie 1")).toBeInTheDocument();
  });
});

describe("deleting from a series", () => {
  async function openSeries(user: ReturnType<typeof userEvent.setup>) {
    renderApp("/library");
    const row = (await screen.findByText("Example Show 1")).closest("tr")!;
    await user.click(within(row).getByRole("button", { name: "Manage" }));
    await screen.findByText("Season 1");
  }

  it("a season is deleted only after its confirm dialog, with confirm=true", async () => {
    const user = userEvent.setup();
    const { calls } = libraryStack();
    await openSeries(user);

    // A season with no files cannot be deleted.
    const buttons = screen.getAllByRole("button", { name: "Delete" }).slice(-2); // the seasons
    expect(buttons[1]).toBeDisabled();
    await user.click(buttons[0]);
    expect(screen.getByText("Delete Season 1?")).toBeInTheDocument();
    expect(deletes(calls)).toEqual([]);

    await user.click(dialog("Delete Season 1?").getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(screen.queryByText("Delete Season 1?")).not.toBeInTheDocument());
    expect(deletes(calls).map((c) => c.url)).toEqual([
      "/api/library/sonarr/5/seasons/1?confirm=true",
    ]);
  });

  it("the whole series is deleted only after its confirm dialog, with confirm=true", async () => {
    const user = userEvent.setup();
    const { calls } = libraryStack();
    await openSeries(user);

    await user.click(screen.getByRole("button", { name: "Delete entire series…" }));
    expect(screen.getByText('Delete "Example Show 1"?')).toBeInTheDocument();
    expect(deletes(calls)).toEqual([]);

    await user.click(dialog('Delete "Example Show 1"?').getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(screen.queryByText("Example Show 1")).not.toBeInTheDocument());
    expect(deletes(calls).map((c) => c.url)).toEqual(["/api/library/sonarr/5?confirm=true"]);
  });

  it("cancelling the season dialog deletes nothing", async () => {
    const user = userEvent.setup();
    const { calls } = libraryStack();
    await openSeries(user);

    await user.click(screen.getAllByRole("button", { name: "Delete" }).slice(-2)[0]);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(deletes(calls)).toEqual([]);
  });
});

describe("confirmedDeleteUrl", () => {
  it("builds the movie, series and season URLs, always with confirm=true", () => {
    expect(confirmedDeleteUrl("radarr", 1)).toBe("/api/library/radarr/1?confirm=true");
    expect(confirmedDeleteUrl("radarr-4k", 12)).toBe("/api/library/radarr-4k/12?confirm=true");
    expect(confirmedDeleteUrl("sonarr", 5, 0)).toBe("/api/library/sonarr/5/seasons/0?confirm=true");
  });

  it("is the only way the UI builds a delete: no other DELETE request exists in the source", () => {
    const files: string[] = [];
    const walk = (dir: string) => {
      for (const name of readdirSync(dir)) {
        const path = join(dir, name);
        if (statSync(path).isDirectory()) walk(path);
        else if (/\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name)) files.push(path);
      }
    };
    walk(resolve(process.cwd(), "src"));

    for (const file of files) {
      // Revoking an own login session is not a library delete.
      if (file.endsWith("AccountPage.tsx")) continue;
      // The administration pages delete services, users and sessions, each behind
      // its own confirm dialog (admin/Admin.test.tsx checks that).
      if (file.includes("/pages/admin/")) continue;
      const text = readFileSync(file, "utf8");
      for (const match of text.matchAll(/method:\s*"DELETE"/g)) {
        const before = text.slice(Math.max(0, match.index - 160), match.index);
        expect(before, `${file} sends a DELETE that does not use confirmedDeleteUrl`).toContain(
          "confirmedDeleteUrl("
        );
      }
    }
  });
});
