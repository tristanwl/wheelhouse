import { NextResponse } from "next/server";
import { getMovies, getSeries } from "@/lib/arr-client";

export const dynamic = "force-dynamic";

export interface LibraryItem {
  service: "sonarr" | "radarr";
  id: number;
  title: string;
  sizeOnDisk: number;
  monitored: boolean;
  rootFolderPath: string;
  added: string;
}

export async function GET() {
  const [series, movies] = await Promise.all([getSeries(), getMovies()]);

  const items: LibraryItem[] = [
    ...series.map((s) => ({
      service: "sonarr" as const,
      id: s.id,
      title: s.title,
      sizeOnDisk: s.statistics?.sizeOnDisk ?? 0,
      monitored: s.monitored,
      rootFolderPath: s.rootFolderPath,
      added: s.added,
    })),
    ...movies.map((m) => ({
      service: "radarr" as const,
      id: m.id,
      title: m.title,
      sizeOnDisk: m.sizeOnDisk ?? 0,
      monitored: m.monitored,
      rootFolderPath: m.rootFolderPath,
      added: m.added,
    })),
  ];

  items.sort((a, b) => b.sizeOnDisk - a.sizeOnDisk);

  return NextResponse.json({ items });
}
