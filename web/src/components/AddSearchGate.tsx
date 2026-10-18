import { useFeatureAvailable } from "@/lib/features";
import { AddSearch } from "./AddSearch";

// The global add/search box needs a media manager to search and add to; that is
// exactly what the library feature requires, so it follows the library's availability.
export function AddSearchGate() {
  if (!useFeatureAvailable("library")) return null;
  return (
    <div className="mb-6">
      <AddSearch />
    </div>
  );
}
