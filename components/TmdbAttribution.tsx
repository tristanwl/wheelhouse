// Required by TMDB's API terms wherever their data is shown.
export function TmdbAttribution() {
  return (
    <p className="pt-2 text-xs text-slate-600">
      This product uses the TMDB API but is not endorsed or certified by{" "}
      <a
        href="https://www.themoviedb.org"
        target="_blank"
        rel="noreferrer"
        className="underline hover:text-slate-400"
      >
        TMDB
      </a>
      .
    </p>
  );
}
