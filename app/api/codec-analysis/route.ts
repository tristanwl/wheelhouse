import { NextResponse } from "next/server";
import { getEpisodeFiles, getEpisodes, getMovies, getSeries, type Episode } from "@/lib/arr-client";
import { analyzeCodecs, type CodecAnalysis, type CodecFileInfo } from "@/lib/codec";
import { getCodecPreferences } from "@/lib/settings";

export const dynamic = "force-dynamic";

export interface CodecAnalysisResponse extends CodecAnalysis {
  // Library items with no file yet, or a file Radarr/Sonarr hasn't scanned
  // mediaInfo for yet — excluded from the analysis rather than guessed at.
  skippedCount: number;
}

async function movieFiles(): Promise<{ files: CodecFileInfo[]; skipped: number }> {
  const movies = await getMovies();
  const files: CodecFileInfo[] = [];
  let skipped = 0;
  for (const m of movies) {
    const codec = m.movieFile?.mediaInfo?.videoCodec;
    if (!m.movieFile || !codec) {
      skipped += 1;
      continue;
    }
    files.push({
      title: m.title,
      codec,
      resolution: m.movieFile.mediaInfo?.resolution ?? null,
      bitDepth: m.movieFile.mediaInfo?.videoBitDepth ?? null,
      size: m.movieFile.size,
      service: "radarr",
      itemId: m.id,
      filePath: m.movieFile.path,
    });
  }
  return { files, skipped };
}

// "S03E07" style, zero-padded to 2 digits — matches how Sonarr itself
// displays episode numbers, not raw integers.
function pad2(n: number): string {
  return String(n).padStart(2, "0");
}

// Sonarr's episodefile resource only carries a season number — episode
// number/title live on the separate `episode` resource, linked back via
// `episodeFileId` (an aired double-episode can share one file, hence an
// array rather than a single match).
function episodeLabel(seriesTitle: string, seasonNumber: number, episodes: Episode[]): string {
  if (episodes.length === 0) {
    // No matching episode record (unusual, but not impossible — e.g. a
    // file imported before Sonarr's episode list caught up) — fall back to
    // the season-only label rather than showing nothing.
    return `${seriesTitle} - Season ${seasonNumber}`;
  }
  const sorted = [...episodes].sort((a, b) => a.episodeNumber - b.episodeNumber);
  const season = pad2(seasonNumber);
  if (sorted.length === 1) {
    const e = sorted[0];
    const suffix = e.title ? ` "${e.title}"` : "";
    return `${seriesTitle} - S${season}E${pad2(e.episodeNumber)}${suffix}`;
  }
  const first = sorted[0];
  const last = sorted[sorted.length - 1];
  return `${seriesTitle} - S${season}E${pad2(first.episodeNumber)}-E${pad2(last.episodeNumber)}`;
}

// Sonarr has no bulk "all episode files"/"all episodes" endpoint — only
// per-series (`/api/v3/episodefile?seriesId=`, `/api/v3/episode?seriesId=`)
// — so this fans out two calls per series in parallel (still fast: ~1s
// end-to-end against the live library, confirmed).
async function tvFiles(): Promise<{ files: CodecFileInfo[]; skipped: number }> {
  const series = await getSeries();
  const perSeries = await Promise.all(
    series.map(async (s) => {
      const [files, episodes] = await Promise.all([getEpisodeFiles(s.id), getEpisodes(s.id)]);
      return { series: s, files, episodes };
    })
  );

  const files: CodecFileInfo[] = [];
  let skipped = 0;
  for (const { series: s, files: episodeFiles, episodes } of perSeries) {
    for (const f of episodeFiles) {
      const codec = f.mediaInfo?.videoCodec;
      if (!codec) {
        skipped += 1;
        continue;
      }
      const matchingEpisodes = episodes.filter((e) => e.episodeFileId === f.id);
      files.push({
        title: episodeLabel(s.title, f.seasonNumber, matchingEpisodes),
        codec,
        resolution: f.mediaInfo?.resolution ?? null,
        bitDepth: f.mediaInfo?.videoBitDepth ?? null,
        size: f.size,
        service: "sonarr",
        itemId: s.id,
        filePath: f.path,
      });
    }
  }
  return { files, skipped };
}

export async function GET(req: Request) {
  const { searchParams } = new URL(req.url);
  const service = searchParams.get("service") === "sonarr" ? "sonarr" : "radarr";

  try {
    const [{ files, skipped }, preferences] = await Promise.all([
      service === "sonarr" ? tvFiles() : movieFiles(),
      getCodecPreferences(),
    ]);
    const response: CodecAnalysisResponse = {
      ...analyzeCodecs(files, preferences),
      skippedCount: skipped,
    };
    return NextResponse.json(response);
  } catch (err) {
    return NextResponse.json(
      { error: err instanceof Error ? err.message : "codec analysis failed" },
      { status: 502 }
    );
  }
}
