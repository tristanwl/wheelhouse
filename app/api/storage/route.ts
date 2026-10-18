import { NextResponse } from "next/server";
import {
  collectFromManagers,
  getMediaManagers,
  KIND_LABEL,
  summarizeManagers,
  type ManagerSummary,
} from "@/lib/providers";

export const dynamic = "force-dynamic";

export interface StorageEntry {
  instanceId: string;
  label: string;
  path: string;
  freeBytes: number;
  libraryUsedBytes: number;
}

export interface StorageResponse {
  entries: StorageEntry[];
  instances: ManagerSummary[];
}

function isWithin(root: string, itemPath: string): boolean {
  const base = root.replace(/\/+$/, "");
  return itemPath === base || itemPath.startsWith(`${base}/`);
}

export async function GET() {
  const entries = await getMediaManagers();
  const { results, problems } = await collectFromManagers(entries, async (client) => {
    const [roots, items] = await Promise.all([
      client.getRootFolders(),
      client.kind === "series"
        ? client.getSeries().then((list) =>
            list.map((s) => ({
              path: s.path ?? s.rootFolderPath,
              size: s.statistics?.sizeOnDisk ?? 0,
            }))
          )
        : client.getMovies().then((list) =>
            list.map((m) => ({
              path: m.path ?? m.rootFolderPath,
              size: m.sizeOnDisk ?? 0,
            }))
          ),
    ]);

    // Each item counts toward the root folder that contains it (the deepest
    // one, if roots nest), not toward every root.
    const used = new Map<string, number>(roots.map((r) => [r.path, 0]));
    for (const item of items) {
      const owner = roots
        .filter((r) => isWithin(r.path, item.path))
        .sort((a, b) => b.path.length - a.path.length)[0];
      if (owner) used.set(owner.path, (used.get(owner.path) ?? 0) + item.size);
    }
    return roots.map((r) => ({ root: r, used: used.get(r.path) ?? 0 }));
  });

  const perKind = new Map<string, number>();
  for (const e of entries) perKind.set(e.kind, (perKind.get(e.kind) ?? 0) + 1);

  const storage: StorageEntry[] = results.flatMap(({ entry, value }) =>
    value.map(({ root, used }) => ({
      instanceId: entry.instanceId,
      // "TV" and "Movies" as before; with several instances of a kind the instance name tells them apart.
      label: (perKind.get(entry.kind) ?? 0) > 1 ? entry.name : KIND_LABEL[entry.kind],
      path: root.path,
      freeBytes: root.freeSpace,
      libraryUsedBytes: used,
    }))
  );

  return NextResponse.json({
    entries: storage,
    instances: summarizeManagers(entries, problems),
  } satisfies StorageResponse);
}
