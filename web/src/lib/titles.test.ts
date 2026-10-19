import { describe, expect, it } from "vitest";
import { titleFor } from "./titles";

describe("titleFor", () => {
  it("names every page", () => {
    expect(titleFor("/")).toBe("Dashboard · wheelhouse");
    expect(titleFor("/library")).toBe("Library · wheelhouse");
    expect(titleFor("/explore/popular")).toBe("Explore · wheelhouse");
    expect(titleFor("/admin/users")).toBe("Users · wheelhouse");
    expect(titleFor("/admin/services/new")).toBe("Services · wheelhouse");
    expect(titleFor("/admin")).toBe("Administration · wheelhouse");
    expect(titleFor("/login")).toBe("Sign in · wheelhouse");
    expect(titleFor("/setup")).toBe("Set up · wheelhouse");
  });

  it("does not confuse a longer path that only starts alike", () => {
    expect(titleFor("/libraryx")).toBe("Page not found · wheelhouse");
    expect(titleFor("/nope")).toBe("Page not found · wheelhouse");
  });
});
