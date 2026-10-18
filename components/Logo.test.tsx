import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { Logo } from "./Logo";

describe("Logo", () => {
  it("renders the lowercase name, shown at first, with an accessible label", () => {
    const html = renderToStaticMarkup(<Logo />);
    expect(html).toContain("wheelhouse");
    expect(html).not.toContain("Wheelhouse");
    expect(html).toContain('aria-label="wheelhouse home"');
    expect(html).toContain('data-open="true"');
    expect(html).toContain('href="/"');
  });
});
