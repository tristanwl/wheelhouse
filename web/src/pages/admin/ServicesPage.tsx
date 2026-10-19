import { useState } from "react";
import { Link, useLocation } from "react-router";
import useSWR from "swr";
import { ConfirmModal } from "@/components/ConfirmModal";
import { useProviders, useRefreshApp } from "@/components/admin/hooks";
import {
  BTN,
  BTN_DANGER,
  BTN_PRIMARY,
  BTN_QUIET,
  CARD,
  errorText,
} from "@/components/admin/styles";
import { Badge, ErrorNote, LiveResult, PageTitle } from "@/components/admin/ui";
import {
  STATUS_TEXT,
  ago,
  type InstancesResponse,
  type Provider,
  type ServiceInstance,
  type TestResult,
} from "@/lib/admin";
import { fetcher } from "@/lib/fetcher";
import { apiJson, jsonInit } from "@/lib/http";

function statusTone(s: ServiceInstance["status"]): "good" | "neutral" | "bad" {
  return s === "ready" ? "good" : s === "disabled" ? "neutral" : "bad";
}

function ServiceRow({
  service,
  provider,
  onChanged,
  onMessage,
}: {
  service: ServiceInstance;
  provider: Provider | undefined;
  onChanged: () => Promise<unknown>;
  onMessage: (m: { text: string; error?: boolean }) => void;
}) {
  const [busy, setBusy] = useState<"test" | "import" | "delete" | null>(null);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const environment = service.source === "environment";

  async function test() {
    setBusy("test");
    setResult(null);
    try {
      const r = await apiJson<TestResult>(
        "/api/admin/instances/test",
        jsonInit("POST", { id: service.id })
      );
      setResult({
        ok: r.ok,
        text: r.ok
          ? `Connection works${r.version ? `, version ${r.version}` : ""}.`
          : `Connection failed: ${r.error || "no reason given"}`,
      });
      await onChanged();
    } catch (err) {
      setResult({ ok: false, text: errorText(err) });
    } finally {
      setBusy(null);
    }
  }

  async function importIt() {
    setBusy("import");
    try {
      await apiJson(
        `/api/admin/instances/${encodeURIComponent(service.id)}/import`,
        jsonInit("POST", {})
      );
      onMessage({
        text: `${service.name} is now managed here. The environment settings for it are ignored from now on.`,
      });
      await onChanged();
    } catch (err) {
      onMessage({ text: errorText(err), error: true });
    } finally {
      setBusy(null);
    }
  }

  async function remove() {
    setBusy("delete");
    try {
      const r = await apiJson<{ environmentAgain?: boolean }>(
        `/api/admin/instances/${encodeURIComponent(service.id)}`,
        { method: "DELETE" }
      );
      onMessage({
        text: r.environmentAgain
          ? `${service.name} was removed. The server's environment settings for it apply again.`
          : `${service.name} was removed.`,
      });
      setConfirmDelete(false);
      await onChanged();
    } catch (err) {
      setConfirmDelete(false);
      onMessage({ text: errorText(err), error: true });
    } finally {
      setBusy(null);
    }
  }

  return (
    <li className={`${CARD} space-y-3 p-4`} aria-label={service.name}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="truncate font-medium">{service.name}</h2>
          <p className="text-xs text-slate-500">
            {provider?.displayName ?? service.providerId} · ID <code>{service.id}</code>
          </p>
        </div>
        <div className="flex flex-wrap gap-1.5">
          <Badge tone={environment ? "warn" : "info"}>
            {environment ? "Source: environment" : "Source: database"}
          </Badge>
          <Badge tone={statusTone(service.status)}>{STATUS_TEXT[service.status]}</Badge>
        </div>
      </div>

      <p className="text-sm text-slate-400">
        {service.lastTest
          ? service.lastTest.ok
            ? `Last test ${ago(service.lastTest.at)}: connection worked.`
            : `Last test ${ago(service.lastTest.at)}: failed. ${service.lastTest.error ?? ""}`
          : "Not tested since the server started."}
      </p>
      {service.error && <p className="text-sm text-red-400">{service.error}</p>}

      {environment && (
        <p className="text-sm text-slate-400">
          This service comes from the server&apos;s environment settings, so it cannot be edited
          here. Import it to manage it from this page; after that the environment settings for it
          are ignored.
        </p>
      )}
      {service.overridesEnvironment && (
        <p className="text-sm text-slate-400">
          This replaces a service of the same ID in the server&apos;s environment settings. Removing
          it makes those settings apply again.
        </p>
      )}

      <div className="flex flex-wrap items-center gap-2">
        {!environment && (
          <Link
            to={`/admin/services/${encodeURIComponent(service.id)}`}
            className={BTN}
            aria-label={`Edit ${service.name}`}
          >
            Edit
          </Link>
        )}
        <button
          type="button"
          className={BTN}
          disabled={busy !== null}
          onClick={() => void test()}
          aria-label={`Test connection to ${service.name}`}
        >
          {busy === "test" ? "Testing…" : "Test connection"}
        </button>
        {environment ? (
          <button
            type="button"
            className={BTN}
            disabled={busy !== null}
            onClick={() => void importIt()}
            aria-label={`Import ${service.name} from the environment`}
          >
            {busy === "import" ? "Importing…" : "Import from environment"}
          </button>
        ) : (
          <button
            type="button"
            className={BTN_DANGER}
            disabled={busy !== null}
            onClick={() => setConfirmDelete(true)}
            aria-label={`Delete ${service.name}`}
          >
            Delete
          </button>
        )}
      </div>
      <LiveResult tone={result?.ok ? "good" : "bad"}>{result?.text}</LiveResult>

      {confirmDelete && (
        <ConfirmModal
          title={`Delete ${service.name}?`}
          body={
            service.overridesEnvironment
              ? "Wheelhouse stops using these stored settings and goes back to the server's environment settings for this service."
              : "Wheelhouse stops using this service and forgets its address and credentials. The service itself is not touched."
          }
          confirmLabel="Delete service"
          busy={busy === "delete"}
          onConfirm={() => void remove()}
          onCancel={() => setConfirmDelete(false)}
        />
      )}
    </li>
  );
}

export default function ServicesPage() {
  const location = useLocation();
  const flash = (location.state as { saved?: string } | null)?.saved;
  const { data, error, mutate } = useSWR<InstancesResponse>("/api/admin/instances", fetcher);
  const { data: providers } = useProviders();
  const refresh = useRefreshApp();
  const [message, setMessage] = useState<{ text: string; error?: boolean } | null>(null);

  async function changed() {
    await Promise.all([mutate(), refresh()]);
  }

  return (
    <div className="space-y-4">
      <PageTitle
        title="Services"
        actions={
          <Link to="/admin/services/new" className={BTN_PRIMARY}>
            Add service
          </Link>
        }
      >
        <p>
          The programs Wheelhouse talks to: Radarr, Sonarr, download clients, media servers and
          TMDB. You can add several of the same kind, for example a second Radarr for 4K. Keys and
          passwords are stored encrypted and are never shown again.
        </p>
      </PageTitle>

      {flash && !message && <LiveResult>{flash}</LiveResult>}
      {message &&
        (message.error ? (
          <ErrorNote>{message.text}</ErrorNote>
        ) : (
          <LiveResult>{message.text}</LiveResult>
        ))}
      {error && <ErrorNote>Could not load the services. {errorText(error, "")}</ErrorNote>}
      {data && data.instances.length === 0 && (
        <div className={`${CARD} space-y-2 p-4 text-sm text-slate-300`}>
          <p>No services yet. Add the first one to get the dashboard going.</p>
          <Link to="/admin/services/new" className={`${BTN_PRIMARY} inline-block`}>
            Add your first service
          </Link>
        </div>
      )}
      <ul className="space-y-3">
        {data?.instances.map((s) => (
          <ServiceRow
            key={s.id}
            service={s}
            provider={providers?.providers.find((p) => p.id === s.providerId)}
            onChanged={changed}
            onMessage={setMessage}
          />
        ))}
      </ul>
      <p className="text-xs text-slate-500">
        <Link to="/" className={BTN_QUIET}>
          Back to the dashboard
        </Link>
      </p>
    </div>
  );
}
