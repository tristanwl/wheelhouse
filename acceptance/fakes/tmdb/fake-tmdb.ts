/** The data the fake TMDB server (server.ts) serves. */

export const GOOD_KEY = "tmdb-key-0123456789abcdef";

export const movie = (id: number) => ({
  id,
  title: `Example Movie ${id}`,
  overview: "A made-up plot.",
  release_date: "2024-01-01",
  poster_path: `/poster${id}.jpg`,
  vote_average: 7.5,
});

export const movieDetail = (id: number) => ({
  ...movie(id),
  genres: [{ id: 1, name: "Drama" }],
  runtime: 100,
  backdrop_path: "/backdrop.jpg",
  videos: { results: [] },
});
