import { Link } from "react-router";

/** Any path the app has no page for. The server answers these with the app shell, so this is the 404. */
export default function NotFoundPage() {
  return (
    <div className="rounded-lg border border-slate-800 bg-slate-900 p-4 text-sm text-slate-300">
      <h1 className="mb-2 text-lg font-medium">Page not found</h1>
      <p>
        There is nothing at this address. Go back to the{" "}
        <Link to="/" className="underline hover:text-white">
          Dashboard
        </Link>
        .
      </p>
    </div>
  );
}
