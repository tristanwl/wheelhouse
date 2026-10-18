import { NextResponse } from "next/server";
import {
  collectFromManagers,
  getMediaManagers,
  summarizeManagers,
  type ManagerKind,
  type ManagerSummary,
} from "@/lib/providers";

export const dynamic = "force-dynamic";

export interface LibraryItem {
  instanceId: string;
  instanceName: string;
  kind: ManagerKind;
  id: number;
  title: string;
  sizeOnDisk: number;
  monitored: boolean;
  rootFolderPath: string;
  added: string;
}

export interface LibraryResponse {
  items: LibraryItem[];
  /** Every configured media manager, so the UI can say which are down or missing. */
  instances: ManagerSummary[];
}

export async function GET() {
  const entries = await getMediaManagers();
  const { results, problems } = await collectFromManagers(entries, async (client, entry) => {
    const base = { instanceId: entry.instanceId, instanceName: entry.name, kind: client.kind };
    if (client.kind === "series") {
      return (await client.getSeries()).map(
        (s): LibraryItem => ({
          ...base,
          id: s.id,
          title: s.title,
          sizeOnDisk: s.statistics?.sizeOnDisk ?? 0,
          monitored: s.monitored,
          rootFolderPath: s.rootFolderPath,
          added: s.added,
        })
      );
    }
    return (await client.getMovies()).map(
      (m): LibraryItem => ({
        ...base,
        id: m.id,
        title: m.title,
        sizeOnDisk: m.sizeOnDisk ?? 0,
        monitored: m.monitored,
        rootFolderPath: m.rootFolderPath,
        added: m.added,
      })
    );
  });

  const items = results.flatMap((r) => r.value);
  items.sort((a, b) => b.sizeOnDisk - a.sizeOnDisk);

  return NextResponse.json({
    items,
    instances: summarizeManagers(entries, problems),
  } satisfies LibraryResponse);
}
