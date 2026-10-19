import type { InstanceRef } from "./types";

export interface LibraryStatus {
  /** The movie is in the instance the user is adding to (or in the only one). */
  inTarget: boolean;
  /** "In library", or "In library: Radarr, Radarr 4K" when there are several instances. */
  label: string;
  /** Set when it is only in other instances: where, for a note beside the Add button. */
  elsewhere: string | null;
}

/**
 * Says where a movie already is, relative to the instance being added to.
 * With one movie instance the answer is the plain boolean; with several, the
 * badge names the instances, and a movie that is only in another instance can
 * still be added to the chosen one.
 */
export function libraryStatus(
  alreadyAdded: boolean,
  libraryInstances: InstanceRef[] | undefined,
  targetId: string | null,
  choices: InstanceRef[]
): LibraryStatus {
  const where = libraryInstances ?? [];
  const names = where.map((i) => i.name).join(", ");
  if (choices.length <= 1 || where.length === 0) {
    return { inTarget: alreadyAdded, label: "In library", elsewhere: null };
  }
  const inTarget = where.some((i) => i.instanceId === targetId);
  return {
    inTarget,
    label: `In library: ${names}`,
    elsewhere: inTarget ? null : `In ${names}`,
  };
}
