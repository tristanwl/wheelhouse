function required(name: string): string {
  const value = process.env[name];
  if (!value) {
    throw new Error(`Missing required env var: ${name}`);
  }
  return value;
}

export const env = {
  get sonarrUrl() {
    return required("SONARR_URL");
  },
  get sonarrApiKey() {
    return required("SONARR_API_KEY");
  },
  get radarrUrl() {
    return required("RADARR_URL");
  },
  get radarrApiKey() {
    return required("RADARR_API_KEY");
  },
  // Optional: the Explore tab degrades to "not configured" without it.
  get tmdbApiKey() {
    return process.env.TMDB_API_KEY;
  },
};
