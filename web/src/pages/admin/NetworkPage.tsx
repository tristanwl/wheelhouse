import { useState } from "react";
import useSWR from "swr";
import { BTN_PRIMARY, CARD, errorText } from "@/components/admin/styles";
import {
  Badge,
  ErrorNote,
  LiveResult,
  PageTitle,
  StringListEditor,
  Switch,
} from "@/components/admin/ui";
import { validateRange, type NetworkSettings } from "@/lib/admin";
import { fetcher } from "@/lib/fetcher";
import { apiJson, jsonInit } from "@/lib/http";

function Warnings({ items }: { items: string[] }) {
  if (items.length === 0) return null;
  return (
    <ul
      aria-label="Warnings"
      className="space-y-1 rounded-md border border-amber-900 bg-amber-950/30 p-3 text-sm text-amber-200"
    >
      {items.map((w) => (
        <li key={w}>{w}</li>
      ))}
    </ul>
  );
}

function NetworkForm({
  settings,
  onSaved,
}: {
  settings: NetworkSettings;
  onSaved: (s: NetworkSettings) => void;
}) {
  const [enabled, setEnabled] = useState(settings.enabled);
  const [cidrs, setCidrs] = useState(settings.cidrs);
  const [proxies, setProxies] = useState(settings.trustedProxies);
  const [warnings, setWarnings] = useState(settings.warnings);
  const [showErrors, setShowErrors] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState<string | null>(null);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setSaved(null);
    setError(null);
    setShowErrors(true);
    const rows = [...cidrs, ...proxies];
    if (rows.some((r) => validateRange(r))) {
      setError("Fix the entries marked below, then save again.");
      return;
    }
    if (enabled && cidrs.length === 0) {
      setError("Add at least one address range before turning local-network access on.");
      return;
    }
    setBusy(true);
    try {
      const next = await apiJson<NetworkSettings>(
        "/api/admin/network",
        jsonInit("PUT", {
          enabled,
          cidrs: cidrs.map((c) => c.trim()),
          trustedProxies: proxies.map((c) => c.trim()),
        })
      );
      setWarnings(next.warnings ?? []);
      setCidrs(next.cidrs);
      setProxies(next.trustedProxies);
      setShowErrors(false);
      onSaved(next);
      setSaved("Network settings saved. They apply right away.");
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }

  const client = settings.client;
  return (
    <form onSubmit={(e) => void save(e)} noValidate className="space-y-5">
      <section className={`${CARD} space-y-4 p-4`}>
        <Switch
          label="Let devices on my local network in without a login"
          checked={enabled}
          onChange={setEnabled}
          disabled={busy}
          description="Off by default. When on, anyone connecting from the ranges below uses Wheelhouse without signing in."
        />
        <p className="text-sm text-slate-400">
          People let in this way get the <strong>user</strong> role: they can use the dashboard,
          library, explore and codecs pages, but they are <strong>never administrators</strong>.
          Settings, accounts and this page always need a real administrator sign-in.
        </p>
        <StringListEditor
          label="Local network ranges"
          hint={
            <>
              Addresses or ranges such as <code>192.168.1.0/24</code> or <code>fd00::/8</code>. Use
              ranges of private addresses only.
            </>
          }
          values={cidrs}
          onChange={setCidrs}
          validate={validateRange}
          placeholder="192.168.1.0/24"
          addLabel="Add a range"
          showErrors={showErrors}
          disabled={busy}
        />
      </section>

      <section className={`${CARD} space-y-4 p-4`}>
        <h2 className="font-medium">Trusted proxies</h2>
        <p className="text-sm text-slate-400">
          If Wheelhouse sits behind a reverse proxy (nginx, Traefik, Caddy), list the proxy&apos;s
          address here so Wheelhouse can see who is really connecting. Only addresses listed here
          are believed when they report a client address; anyone else who sends such a header is
          ignored. Leave this empty if people reach Wheelhouse directly.
        </p>
        <StringListEditor
          label="Trusted proxy addresses"
          values={proxies}
          onChange={setProxies}
          validate={validateRange}
          placeholder="172.18.0.2"
          addLabel="Add a proxy"
          showErrors={showErrors}
          disabled={busy}
        />
        {settings.envTrustedProxies && settings.envTrustedProxies.length > 0 && (
          <p className="text-xs text-slate-500">
            Also trusted from the server&apos;s environment settings (not editable here):{" "}
            {settings.envTrustedProxies.join(", ")}.
          </p>
        )}
      </section>

      {client && (
        <section aria-label="Your connection" className={`${CARD} space-y-1 p-4 text-sm`}>
          <h2 className="font-medium">Your connection</h2>
          <p className="text-slate-300">
            Wheelhouse sees you connecting from <code>{client.address}</code>
            {client.forwarded ? " (reported by a trusted proxy)" : ""}.{" "}
            {client.inRange ? (
              <Badge tone="good">Inside the ranges above</Badge>
            ) : (
              <Badge tone="neutral">Outside the ranges above</Badge>
            )}
          </p>
          <p className="text-xs text-slate-500">
            This is the saved setting. If it is not what you expect, a proxy may be hiding your real
            address: add it as a trusted proxy.
          </p>
        </section>
      )}

      <Warnings items={warnings} />
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="flex items-center gap-3">
        <button type="submit" disabled={busy} className={BTN_PRIMARY}>
          {busy ? "Saving…" : "Save network settings"}
        </button>
        <LiveResult>{saved}</LiveResult>
      </div>
    </form>
  );
}

export default function NetworkPage() {
  const { data, error, mutate } = useSWR<NetworkSettings>("/api/admin/network", fetcher);
  return (
    <div className="space-y-4">
      <PageTitle title="Network">
        <p>
          Who can use Wheelhouse without signing in, and which proxies to believe about who is
          connecting.
        </p>
      </PageTitle>
      {error && <ErrorNote>Could not load the network settings. {errorText(error, "")}</ErrorNote>}
      {data && (
        <NetworkForm
          settings={{
            ...data,
            cidrs: data.cidrs ?? [],
            trustedProxies: data.trustedProxies ?? [],
            warnings: data.warnings ?? [],
          }}
          onSaved={(next) => void mutate(next, { revalidate: true })}
        />
      )}
    </div>
  );
}
