import { env } from "./env";

export interface SabnzbdQueue {
  status: string;
  paused: boolean;
  kbpersec: string;
  mb: string;
  mbleft: string;
  noofslots_total: number;
  timeleft: string;
  have_warnings: string;
  slots: Array<{
    nzo_id: string;
    filename: string;
    status: string;
    percentage: string;
    mb: string;
    mbleft: string;
    timeleft: string;
  }>;
}

export interface SabnzbdServer {
  name: string;
  host: string;
  port: number;
  enable: number;
  connections: number;
  ssl: number;
}

export interface SabnzbdWarning {
  type: string;
  text: string;
  time: number;
}

export interface SabnzbdHistorySlot {
  nzo_id: string;
  name: string;
  status: string;
}

const POSTPROC_DONE_STATUSES = new Set(["Completed", "Failed"]);

async function sabnzbdApi<T>(mode: string, extraParams = ""): Promise<T> {
  const url = `${env.sabnzbdUrl}/api?mode=${mode}&output=json&apikey=${env.sabnzbdApiKey}${extraParams}`;
  const res = await fetch(url, { cache: "no-store" });
  if (!res.ok) {
    throw new Error(`sabnzbd ${mode} failed: ${res.status} ${res.statusText}`);
  }
  return (await res.json()) as T;
}

export async function getSabnzbdQueue(): Promise<SabnzbdQueue> {
  const data = await sabnzbdApi<{ queue: SabnzbdQueue }>("queue");
  return data.queue;
}

export async function getSabnzbdServers(): Promise<SabnzbdServer[]> {
  const data = await sabnzbdApi<{ config: { servers: SabnzbdServer[] } }>(
    "get_config",
    "&section=servers"
  );
  return data.config.servers;
}

export async function getSabnzbdWarnings(): Promise<SabnzbdWarning[]> {
  const data = await sabnzbdApi<{ warnings: SabnzbdWarning[] }>("warnings");
  return data.warnings;
}

// mode=queue's per-slot `status` collapses every post-download phase
// (Extracting/Verifying/Repairing/Moving/Running) down to "Downloading" —
// that's baked into SABnzbd's own API (see build_queue in api.py), not a
// bug on our end. The accurate per-item status only shows up in
// mode=history, which SABnzbd documents as combining "the jobs still in
// post-processing and the database history". Fetch the still-active ones
// (anything not Completed/Failed) so the queue view can be corrected.
export async function getSabnzbdActivePostProc(): Promise<
  Map<string, string>
> {
  const data = await sabnzbdApi<{ history: { slots: SabnzbdHistorySlot[] } }>(
    "history",
    "&limit=25"
  );
  const active = new Map<string, string>();
  for (const slot of data.history.slots) {
    if (!POSTPROC_DONE_STATUSES.has(slot.status)) {
      active.set(slot.nzo_id, slot.status);
    }
  }
  return active;
}
