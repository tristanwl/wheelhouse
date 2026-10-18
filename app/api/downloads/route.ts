import { NextResponse } from "next/server";
import {
  getRuntime,
  redact,
  type DownloadClient,
  type DownloadItem,
  type DownloadProtocol,
} from "@/lib/providers";

export const dynamic = "force-dynamic";

export interface DownloadsInstance {
  instanceId: string;
  name: string;
  protocol?: DownloadProtocol;
  up: boolean;
  paused?: boolean;
  speedBytesPerSec?: number;
  error?: string;
}

export type DownloadRow = DownloadItem & { instanceId: string };

export interface DownloadsResponse {
  /** One entry per configured download client, working or not. */
  instances: DownloadsInstance[];
  items: DownloadRow[];
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? redact(err.message) : "unknown error";
}

export async function GET() {
  const runtime = getRuntime();
  const instances: DownloadsInstance[] = [];
  const items: DownloadRow[] = [];

  const all = await runtime.listInstances();
  const downloadClients = all.filter((r) => r.definition?.role === "downloadClient");

  await Promise.all(
    downloadClients.map(async (resolved) => {
      const { instance, definition } = resolved;
      const base = {
        instanceId: instance.id,
        name: instance.name,
        protocol: definition?.capabilities.protocol as DownloadProtocol | undefined,
      };
      if (resolved.status !== "ready") {
        // Misconfigured or disabled: say so rather than pretending it isn't there.
        if (resolved.status !== "disabled") {
          instances.push({ ...base, up: false, error: resolved.error ?? resolved.status });
        }
        return;
      }
      try {
        const client = await runtime.getClient<DownloadClient>(instance.id);
        const [status, clientItems] = await Promise.all([client.getStatus(), client.listItems()]);
        instances.push({
          ...base,
          up: true,
          paused: status.paused,
          speedBytesPerSec: status.speedBytesPerSec,
        });
        items.push(...clientItems.map((item) => ({ ...item, instanceId: instance.id })));
      } catch (err) {
        instances.push({ ...base, up: false, error: errorMessage(err) });
      }
    })
  );

  // Promise.all finishes in any order; keep the configured order.
  const order = new Map(downloadClients.map((r, i) => [r.instance.id, i]));
  instances.sort((a, b) => order.get(a.instanceId)! - order.get(b.instanceId)!);
  items.sort((a, b) => order.get(a.instanceId)! - order.get(b.instanceId)!);

  return NextResponse.json({ instances, items } satisfies DownloadsResponse);
}
