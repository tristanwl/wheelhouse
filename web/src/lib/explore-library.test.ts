import { describe, expect, it } from "vitest";
import { libraryStatus } from "./explore-library";

const hd = { instanceId: "radarr", name: "Radarr" };
const uhd = { instanceId: "radarr-4k", name: "Radarr 4K" };

describe("libraryStatus", () => {
  it("is the plain flag with one movie instance", () => {
    expect(libraryStatus(true, [hd], "radarr", [hd])).toEqual({
      inTarget: true,
      label: "In library",
      elsewhere: null,
    });
    expect(libraryStatus(false, [], "radarr", [hd]).inTarget).toBe(false);
  });

  it("names the instances when there are several", () => {
    const s = libraryStatus(true, [hd, uhd], "radarr-4k", [hd, uhd]);
    expect(s).toEqual({ inTarget: true, label: "In library: Radarr, Radarr 4K", elsewhere: null });
  });

  it("lets a movie held only by another instance be added to the chosen one", () => {
    const s = libraryStatus(true, [hd], "radarr-4k", [hd, uhd]);
    expect(s.inTarget).toBe(false);
    expect(s.elsewhere).toBe("In Radarr");
  });

  it("falls back to the flag when an older server sends no instance list", () => {
    expect(libraryStatus(true, undefined, "radarr", [hd, uhd]).inTarget).toBe(true);
  });

  it("says nothing for a movie in no instance", () => {
    expect(libraryStatus(false, [], "radarr", [hd, uhd])).toMatchObject({
      inTarget: false,
      elsewhere: null,
    });
  });
});
