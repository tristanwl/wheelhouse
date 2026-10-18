import { stat } from "node:fs/promises";

/**
 * How many directory entries point at this file's data. A library file with
 * more than one is often also the copy a torrent client is seeding. null when
 * the file can't be read (the conversion itself reports that).
 */
export async function linkCount(filePath: string): Promise<number | null> {
  try {
    return (await stat(filePath)).nlink;
  } catch {
    return null;
  }
}
