import { useId, useMemo, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import useSWR from "swr";
import { SchemaForm } from "@/components/admin/SchemaForm";
import { useProviders, useRefreshApp } from "@/components/admin/hooks";
import { BTN, BTN_PRIMARY, BTN_QUIET, CARD, INPUT, errorText } from "@/components/admin/styles";
import { ErrorNote, LiveResult, PageTitle } from "@/components/admin/ui";
import {
  ROLE_HEADINGS,
  ROLE_ORDER,
  buildConfig,
  deriveInstanceId,
  fieldsOf,
  emptyValues,
  validateAll,
  validateInstanceId,
  valuesOf,
  type FormValue,
  type InstancesResponse,
  type Provider,
  type SecretState,
  type ServiceInstance,
  type TestResult,
} from "@/lib/admin";
import { fetcher } from "@/lib/fetcher";
import { ApiError, apiJson, jsonInit } from "@/lib/http";

interface BodyProps {
  provider: Provider;
  /** Present when editing a saved service. */
  instance?: ServiceInstance;
  takenIds: ReadonlySet<string>;
}

/** The form for one service: name, id (new only), the provider's settings, test and save. */
function ServiceFormBody({ provider, instance, takenIds }: BodyProps) {
  const navigate = useNavigate();
  const refresh = useRefreshApp();
  const fields = useMemo(() => fieldsOf(provider.configSchema), [provider]);
  const editing = instance !== undefined;

  const [name, setName] = useState(instance?.name ?? provider.displayName);
  const [customId, setCustomId] = useState("");
  const [enabled, setEnabled] = useState(instance?.enabled ?? true);
  const [values, setValues] = useState(() =>
    instance ? valuesOf(fields, instance.config) : emptyValues(fields)
  );
  const [secrets, setSecrets] = useState<Record<string, SecretState>>(() =>
    instance
      ? Object.fromEntries(
          fields
            .filter((f) => f.kind === "secret")
            .map((f) => {
              const stored = instance.secrets[f.name]?.set === true;
              return [f.name, { stored, replacing: !stored }];
            })
        )
      : {}
  );
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null);
  const nameId = useId();
  const idId = useId();

  const derived = editing ? instance.id : deriveInstanceId(provider.id, name, takenIds);

  /** Our own checks; true when the form can be sent. */
  function check(forTest: boolean): boolean {
    const next = validateAll(fields, values, secrets);
    if (!forTest) {
      if (name.trim() === "") next.__name = "Give the service a name.";
      if (!editing) {
        const idProblem = validateInstanceId(provider.id, customId.trim());
        if (idProblem) next.__id = idProblem;
        else if (customId.trim() === "" && !derived) {
          next.__id = "Both default IDs are taken. Enter an ID of your own.";
        } else if (customId.trim() !== "" && takenIds.has(customId.trim())) {
          next.__id = "A service with that ID already exists.";
        }
      }
    }
    setErrors(next);
    setFormError(
      Object.keys(next).length > 0 ? "Fix the problems marked below, then try again." : null
    );
    return Object.keys(next).length === 0;
  }

  function showServerError(err: unknown) {
    if (err instanceof ApiError && err.fields) {
      const next: Record<string, string> = {};
      for (const f of err.fields) next[f.field] = f.message;
      setErrors(next);
      setFormError("The server did not accept some settings. They are marked below.");
    } else {
      setFormError(errorText(err));
    }
  }

  async function runTest() {
    if (!check(true)) return;
    setBusy("test");
    setTest(null);
    try {
      const config = buildConfig(fields, values, secrets);
      const body = editing ? { id: instance.id, config } : { providerId: provider.id, config };
      const r = await apiJson<TestResult>("/api/admin/instances/test", jsonInit("POST", body));
      setTest({
        ok: r.ok,
        text: r.ok
          ? `Connection works${r.version ? `, version ${r.version}` : ""}.`
          : `Connection failed: ${r.error || "no reason given"}`,
      });
    } catch (err) {
      if (err instanceof ApiError && err.fields) showServerError(err);
      else setTest({ ok: false, text: errorText(err) });
    } finally {
      setBusy(null);
    }
  }

  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!check(false)) return;
    setBusy("save");
    setFormError(null);
    try {
      const config = buildConfig(fields, values, secrets);
      if (editing) {
        await apiJson(
          `/api/admin/instances/${encodeURIComponent(instance.id)}`,
          jsonInit("PUT", { name: name.trim(), enabled, config })
        );
      } else {
        await apiJson(
          "/api/admin/instances",
          jsonInit("POST", {
            providerId: provider.id,
            ...(customId.trim() ? { id: customId.trim() } : {}),
            name: name.trim(),
            enabled,
            config,
          })
        );
      }
      await refresh();
      void navigate("/admin/services", {
        state: { saved: editing ? `${name.trim()} was saved.` : `${name.trim()} was added.` },
      });
    } catch (err) {
      showServerError(err);
    } finally {
      setBusy(null);
    }
  }

  return (
    <form onSubmit={(e) => void save(e)} noValidate className={`${CARD} space-y-5 p-4`}>
      <div className="space-y-1">
        <label htmlFor={nameId} className="block text-sm text-slate-300">
          Name
        </label>
        <input
          id={nameId}
          value={name}
          onChange={(e) => setName(e.target.value)}
          autoComplete="off"
          aria-invalid={errors.__name ? true : undefined}
          aria-describedby={errors.__name ? `${nameId}-e` : undefined}
          className={INPUT}
        />
        {errors.__name && (
          <p id={`${nameId}-e`} className="text-xs text-red-400">
            {errors.__name}
          </p>
        )}
        <p className="text-xs text-slate-500">
          How it is named on the dashboard, for example &quot;Radarr 4K&quot;.
        </p>
      </div>

      <div className="space-y-1">
        {editing ? (
          <p className="text-sm text-slate-300">
            ID: <code>{instance.id}</code>{" "}
            <span className="text-xs text-slate-500">(cannot be changed)</span>
          </p>
        ) : (
          <>
            <label htmlFor={idId} className="block text-sm text-slate-300">
              ID <span className="text-slate-500">(optional)</span>
            </label>
            <input
              id={idId}
              value={customId}
              onChange={(e) => setCustomId(e.target.value)}
              autoComplete="off"
              spellCheck={false}
              placeholder={derived ?? `${provider.id}-4k`}
              aria-invalid={errors.__id ? true : undefined}
              aria-describedby={`${idId}-h`}
              className={INPUT}
            />
            {errors.__id && <p className="text-xs text-red-400">{errors.__id}</p>}
            <p id={`${idId}-h`} className="text-xs text-slate-500">
              A short name used in links and the audit log. It must be &quot;{provider.id}&quot; or
              start with &quot;{provider.id}-&quot;. Left empty, Wheelhouse uses{" "}
              {customId.trim() ? (
                <code>{customId.trim()}</code>
              ) : derived ? (
                <code>{derived}</code>
              ) : (
                "one derived from the name"
              )}
              . It cannot be changed later.
            </p>
          </>
        )}
      </div>

      <SchemaForm
        fields={fields}
        values={values}
        onChange={(n: string, v: FormValue) => {
          setValues((cur) => ({ ...cur, [n]: v }));
          setErrors((cur) => {
            const { [n]: _gone, ...rest } = cur;
            void _gone;
            return rest;
          });
        }}
        secrets={editing ? secrets : undefined}
        onReplaceSecret={(n, replacing) =>
          setSecrets((cur) => ({ ...cur, [n]: { ...cur[n], replacing } }))
        }
        errors={errors}
        disabled={busy !== null}
      />

      <label className="flex items-center gap-2 text-sm text-slate-300">
        <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
        Use this service (untick to keep the settings but switch it off)
      </label>

      {formError && <ErrorNote>{formError}</ErrorNote>}

      <div className="flex flex-wrap items-center gap-3">
        <button type="submit" disabled={busy !== null} className={BTN_PRIMARY}>
          {busy === "save" ? "Saving…" : editing ? "Save changes" : "Add service"}
        </button>
        <button
          type="button"
          disabled={busy !== null}
          onClick={() => void runTest()}
          className={BTN}
        >
          {busy === "test" ? "Testing…" : "Test connection"}
        </button>
        <Link to="/admin/services" className={BTN_QUIET}>
          Cancel
        </Link>
      </div>
      <LiveResult tone={test?.ok ? "good" : "bad"}>{test?.text}</LiveResult>
      <p className="text-xs text-slate-500">
        Testing uses the values above without saving them.
        {editing && " A stored key or password you have not replaced is used as it is."}
      </p>
    </form>
  );
}

export default function ServiceFormPage() {
  const { id } = useParams();
  const [search] = useSearchParams();
  const editing = id !== undefined;
  const { data: providers, error: providersError } = useProviders();
  const { data: list, error: listError } = useSWR<InstancesResponse>(
    "/api/admin/instances",
    fetcher
  );
  const [chosen, setChosen] = useState(search.get("provider") ?? "");
  const pickId = useId();

  const taken = useMemo(() => new Set((list?.instances ?? []).map((i) => i.id)), [list]);
  const instance = editing ? list?.instances.find((i) => i.id === id) : undefined;

  if (providersError || listError) {
    return (
      <ErrorNote>
        Could not load the services. {errorText(providersError ?? listError, "")}
      </ErrorNote>
    );
  }
  if (!providers || !list) return <p className="text-sm text-slate-400">Loading…</p>;

  if (editing) {
    const provider = instance && providers.providers.find((p) => p.id === instance.providerId);
    return (
      <div className="space-y-4">
        <PageTitle title={instance ? `Edit ${instance.name}` : "Service not found"}>
          {instance?.source === "environment" && (
            <p>
              This service comes from the server&apos;s environment settings and is read-only.
              Import it on the Services page to edit it here.
            </p>
          )}
        </PageTitle>
        {!instance && (
          <p className="text-sm text-slate-300">
            There is no service with the ID <code>{id}</code>.{" "}
            <Link to="/admin/services" className="underline">
              Back to the services
            </Link>
          </p>
        )}
        {instance && instance.source === "database" && !provider && (
          <ErrorNote>
            This service&apos;s type is not known to this version, so it can only be deleted from
            the Services page.
          </ErrorNote>
        )}
        {instance && instance.source === "database" && provider && (
          <ServiceFormBody
            key={instance.id}
            provider={provider}
            instance={instance}
            takenIds={taken}
          />
        )}
      </div>
    );
  }

  const provider = providers.providers.find((p) => p.id === chosen);
  return (
    <div className="space-y-4">
      <PageTitle title="Add a service">
        <p>
          Choose what kind of service it is, enter where to find it and how to sign in, and test the
          connection before you save. To add a second one of the same kind (a Radarr for 4K, say),
          choose the same type again and give it its own name.
        </p>
      </PageTitle>
      <div className={`${CARD} space-y-1 p-4`}>
        <label htmlFor={pickId} className="block text-sm text-slate-300">
          Type of service
        </label>
        <select
          id={pickId}
          value={chosen}
          onChange={(e) => setChosen(e.target.value)}
          className={INPUT}
        >
          <option value="">Choose…</option>
          {ROLE_ORDER.map((role) => {
            const group = providers.providers.filter((p) => p.role === role);
            if (group.length === 0) return null;
            return (
              <optgroup key={role} label={ROLE_HEADINGS[role]}>
                {group.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.displayName}
                  </option>
                ))}
              </optgroup>
            );
          })}
        </select>
      </div>
      {provider && <ServiceFormBody key={provider.id} provider={provider} takenIds={taken} />}
    </div>
  );
}
