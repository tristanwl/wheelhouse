import { NextResponse } from "next/server";
import { getMovies, getRootFolders, getSeries } from "@/lib/arr-client";

export const dynamic = "force-dynamic";

interface StorageEntry {
  label: string;
  path: string;
  freeBytes: number;
  libraryUsedBytes: number;
}

export async function GET() {
  const [sonarrRoots, radarrRoots, series, movies] = await Promise.all([
    getRootFolders("sonarr"),
    getRootFolders("radarr"),
    getSeries(),
    getMovies(),
  ]);

  const tvUsed = series.reduce((sum, s) => sum + (s.statistics?.sizeOnDisk ?? 0), 0);
  const moviesUsed = movies.reduce((sum, m) => sum + (m.sizeOnDisk ?? 0), 0);

  const entries: StorageEntry[] = [
    ...sonarrRoots.map((r) => ({
      label: "TV",
      path: r.path,
      freeBytes: r.freeSpace,
      libraryUsedBytes: tvUsed,
    })),
    ...radarrRoots.map((r) => ({
      label: "Movies",
      path: r.path,
      freeBytes: r.freeSpace,
      libraryUsedBytes: moviesUsed,
    })),
  ];

  return NextResponse.json({ entries });
}
