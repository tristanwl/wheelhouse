export const env = {
  // Optional: the Explore tab degrades to "not configured" without it.
  get tmdbApiKey() {
    return process.env.TMDB_API_KEY;
  },
};
