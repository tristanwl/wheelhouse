import { describe, expect, it } from "vitest";
import fixture from "@/fixtures/providers.json";
import {
  buildConfig,
  deriveInstanceId,
  emptyValues,
  fieldsOf,
  slugify,
  validateAll,
  validateField,
  validateInstanceId,
  validateMediaRoot,
  validateRange,
  valuesOf,
  type ProvidersResponse,
} from "./admin";
import { describeDetail, actionLabel } from "./audit";
import { generatePassword } from "./password";

// The schemas the Go registry really produces (a Go test keeps this file equal to it).
const providers = (fixture as unknown as ProvidersResponse).providers;
const byId = (id: string) => providers.find((p) => p.id === id)!;

describe("forms generated from the real provider schemas", () => {
  it("covers every provider the server registers", () => {
    expect(providers.map((p) => p.id).sort()).toEqual([
      "jellyfin",
      "plex",
      "qbittorrent",
      "radarr",
      "sabnzbd",
      "sonarr",
      "tmdb",
    ]);
  });

  it.each(providers.map((p) => [p.id, p] as const))(
    "%s: one field per property in declared order; secrets are secret fields",
    (_id, p) => {
      const fields = fieldsOf(p.configSchema);
      expect(fields.map((f) => f.name)).toEqual(p.configSchema["x-order"]);
      expect(fields.filter((f) => f.kind === "secret").map((f) => f.name)).toEqual(
        (p.secretKeys ?? []).filter((k) => fields.some((f) => f.name === k))
      );
      for (const f of fields) {
        expect(f.required).toBe((p.configSchema.required ?? []).includes(f.name));
        expect(f.label).not.toBe("");
      }
    }
  );

  it("treats uri properties as address fields, and writeOnly ones as secrets", () => {
    const radarr = fieldsOf(byId("radarr").configSchema);
    expect(radarr.find((f) => f.name === "url")?.kind).toBe("url");
    expect(radarr.find((f) => f.name === "apiKey")?.kind).toBe("secret");
    const qb = fieldsOf(byId("qbittorrent").configSchema);
    expect(qb.find((f) => f.name === "username")?.kind).toBe("text");
    expect(qb.find((f) => f.name === "password")?.kind).toBe("secret");
  });

  it("falls back to the property names when a schema has no order, and handles booleans and integers", () => {
    const fields = fieldsOf({
      properties: {
        b: { type: "boolean", title: "Verify" },
        n: { type: "integer", title: "Port" },
      },
      required: ["n"],
    });
    expect(fields.map((f) => [f.name, f.kind, f.required])).toEqual([
      ["b", "boolean", false],
      ["n", "integer", true],
    ]);
    expect(validateField(fields[1], "80x")).toMatch(/whole number/);
    expect(validateField(fields[1], "80")).toBeUndefined();
    expect(buildConfig(fields, { b: true, n: "8080" })).toEqual({ b: true, n: 8080 });
  });
});

describe("validation", () => {
  const radarr = fieldsOf(byId("radarr").configSchema);

  it("requires what the schema requires, and names the setting", () => {
    expect(validateAll(radarr, emptyValues(radarr), {})).toEqual({
      url: "URL is required.",
      apiKey: "API key is required.",
    });
  });

  it("checks addresses: scheme, shape and no credentials in the URL", () => {
    const url = radarr[0];
    expect(validateField(url, "radarr:7878")).toMatch(/http:\/\/ or https:\/\//);
    expect(validateField(url, "http://")).toMatch(/not a valid address/);
    expect(validateField(url, "http://user:pw@192.0.2.1")).toMatch(/must not contain a username/);
    expect(validateField(url, "http://192.0.2.1:7878")).toBeUndefined();
    expect(validateField(url, "https://example.com/radarr")).toBeUndefined();
  });

  it("does not ask for a stored secret again, but does when it is being replaced", () => {
    const key = radarr[1];
    expect(validateField(key, "", { stored: true, replacing: false })).toBeUndefined();
    expect(validateField(key, "", { stored: true, replacing: true })).toMatch(/required/);
  });

  it("optional settings may be empty", () => {
    const tmdb = fieldsOf(byId("tmdb").configSchema);
    const url = tmdb.find((f) => f.name === "url")!;
    expect(url.required).toBe(false);
    expect(validateField(url, "")).toBeUndefined();
  });
});

describe("building the request", () => {
  const radarr = fieldsOf(byId("radarr").configSchema);

  it("a new service sends what was typed, trimmed", () => {
    expect(
      buildConfig(radarr, { url: " http://192.0.2.1:7878 ", apiKey: "example-key-1" })
    ).toEqual({ url: "http://192.0.2.1:7878", apiKey: "example-key-1" });
  });

  it("a saved service leaves a secret out unless it is replaced", () => {
    const values = { url: "http://192.0.2.1:7878", apiKey: "" };
    expect(buildConfig(radarr, values, { apiKey: { stored: true, replacing: false } })).toEqual({
      url: "http://192.0.2.1:7878",
    });
    expect(
      buildConfig(
        radarr,
        { ...values, apiKey: "example-key-2" },
        { apiKey: { stored: true, replacing: true } }
      )
    ).toEqual({ url: "http://192.0.2.1:7878", apiKey: "example-key-2" });
  });

  it("a secret that is typed but not being replaced is not sent", () => {
    expect(
      buildConfig(
        radarr,
        { url: "http://192.0.2.1:7878", apiKey: "left-in-the-field" },
        { apiKey: { stored: true, replacing: false } }
      )
    ).toEqual({ url: "http://192.0.2.1:7878" });
  });

  it("an empty optional setting is left out, which clears it", () => {
    const tmdb = fieldsOf(byId("tmdb").configSchema);
    expect(buildConfig(tmdb, { apiKey: "example-key-3", url: "" })).toEqual({
      apiKey: "example-key-3",
    });
  });

  it("never reads a secret back from a saved service", () => {
    const values = valuesOf(radarr, { url: "http://192.0.2.1:7878", apiKey: "leaked-by-mistake" });
    expect(values.apiKey).toBe("");
  });
});

describe("instance ids", () => {
  it("follows the server's rules", () => {
    expect(validateInstanceId("radarr", "")).toBeUndefined();
    expect(validateInstanceId("radarr", "radarr")).toBeUndefined();
    expect(validateInstanceId("radarr", "radarr-4k")).toBeUndefined();
    expect(validateInstanceId("radarr", "sonarr-x")).toMatch(/must be "radarr" or start with/);
    expect(validateInstanceId("radarr", "Radarr-4K")).toMatch(/lowercase/);
    expect(validateInstanceId("radarr", "radarr--4k")).toMatch(/single hyphens/);
    expect(validateInstanceId("radarr", `radarr-${"a".repeat(60)}`)).toMatch(/at most 64/);
  });

  it("derives the id like the server: the provider id, then provider-name", () => {
    expect(deriveInstanceId("radarr", "Radarr", new Set())).toBe("radarr");
    expect(deriveInstanceId("radarr", "Radarr 4K", new Set(["radarr"]))).toBe("radarr-radarr-4k");
    expect(deriveInstanceId("radarr", "4K movies", new Set(["radarr"]))).toBe("radarr-4k-movies");
    expect(deriveInstanceId("radarr", "!!!", new Set(["radarr"]))).toBeUndefined();
    expect(deriveInstanceId("radarr", "Kids", new Set(["radarr", "radarr-kids"]))).toBeUndefined();
    expect(slugify("  Radarr (4K)  ")).toBe("radarr-4k");
  });
});

describe("network and folder entries", () => {
  it("accepts addresses and ranges, refuses the rest", () => {
    for (const ok of ["192.168.1.0/24", "10.0.0.5", "fd00::/8", "::1", "100.64.0.0/10"]) {
      expect(validateRange(ok), ok).toBeUndefined();
    }
    expect(validateRange("")).toMatch(/Enter an address/);
    expect(validateRange("192.168.1.256")).toMatch(/0 to 255/);
    expect(validateRange("192.168.1.0/33")).toMatch(/0 to 32/);
    expect(validateRange("0.0.0.0/0")).toMatch(/every address/);
    expect(validateRange("my-laptop")).toMatch(/not an address/);
    expect(validateRange("1.2.3.4/5/6")).toMatch(/not an address/);
  });

  it("media folders are absolute paths", () => {
    expect(validateMediaRoot("/movies")).toBeUndefined();
    expect(validateMediaRoot("movies")).toMatch(/starting with a slash/);
    expect(validateMediaRoot("  ")).toMatch(/Enter a folder/);
  });
});

describe("generated passwords", () => {
  it("are long, random and free of look-alike characters", () => {
    const a = generatePassword();
    const b = generatePassword();
    expect(a).toHaveLength(20);
    expect(a).not.toBe(b);
    expect(a).toMatch(/^[abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789]+$/);
  });
});

describe("audit text", () => {
  it("labels known actions and leaves unknown ones as they are", () => {
    expect(actionLabel("user.create")).toBe("User created");
    expect(actionLabel("something.new")).toBe("something.new");
  });

  it("renders details as short readable text", () => {
    expect(describeDetail({ provider: "radarr", enabled: true, fields: ["url"] })).toBe(
      "type: radarr; on: yes; settings changed: url"
    );
    expect(describeDetail(null)).toBe("");
    expect(describeDetail({ cidrs: [] })).toBe("ranges: none");
  });
});
