import { act, fireEvent, render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Logo } from "./Logo";

function Where() {
  return <output aria-label="location">{useLocation().pathname}</output>;
}

/** The logo on /library, so a click that follows the link is seen as a move to "/". */
function setup() {
  render(
    <MemoryRouter initialEntries={["/library"]}>
      <Logo />
      <Where />
    </MemoryRouter>
  );
  return screen.getByRole("link", { name: "wheelhouse home" });
}

const where = () => screen.getByLabelText("location").textContent;

const open = (el: HTMLElement) => el.getAttribute("data-open");
const wheel = (el: HTMLElement) => el.querySelector("svg") as SVGElement;

/** A pointer event of the given type; jsdom's fireEvent helpers do not carry pointerType. */
function pointer(el: HTMLElement, type: string, pointerType: string) {
  act(() => {
    el.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true, pointerType }));
  });
}

describe("Logo", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("renders the lowercase name, shown at first, as a link home", () => {
    const logo = setup();
    expect(logo).toHaveAttribute("href", "/");
    expect(logo).toHaveTextContent("wheelhouse");
    expect(logo.textContent).not.toContain("Wheelhouse");
    expect(open(logo)).toBe("true");
  });

  it("winds the name in after 3.5 seconds, turning the wheel back", () => {
    const logo = setup();
    act(() => void vi.advanceTimersByTime(3499));
    expect(open(logo)).toBe("true");
    expect(wheel(logo).style.transform).toBe("rotate(0deg)");
    act(() => void vi.advanceTimersByTime(1));
    expect(open(logo)).toBe("false");
    expect(wheel(logo).style.transform).toBe("rotate(-360deg)");
  });

  it("rolls out on hover and winds in 600 ms after the pointer leaves", () => {
    const logo = setup();
    act(() => void vi.advanceTimersByTime(3500));
    expect(open(logo)).toBe("false");

    pointer(logo, "pointerover", "mouse");
    expect(open(logo)).toBe("true");
    expect(wheel(logo).style.transform).toBe("rotate(0deg)");

    pointer(logo, "pointerout", "mouse");
    act(() => void vi.advanceTimersByTime(599));
    expect(open(logo)).toBe("true");
    act(() => void vi.advanceTimersByTime(1));
    expect(open(logo)).toBe("false");
  });

  it("hovering during the intro cancels the pending wind-in", () => {
    const logo = setup();
    act(() => void vi.advanceTimersByTime(2000));
    pointer(logo, "pointerover", "mouse");
    act(() => void vi.advanceTimersByTime(10_000));
    expect(open(logo)).toBe("true");
  });

  it("rolls out on keyboard focus and winds in after blur", () => {
    const logo = setup();
    act(() => void vi.advanceTimersByTime(3500));
    expect(open(logo)).toBe("false");

    fireEvent.focus(logo);
    expect(open(logo)).toBe("true");
    fireEvent.blur(logo);
    act(() => void vi.advanceTimersByTime(600));
    expect(open(logo)).toBe("false");
  });

  it("a first touch tap reveals the name instead of following the link, then it winds in", () => {
    const logo = setup();
    act(() => void vi.advanceTimersByTime(3500));
    expect(open(logo)).toBe("false");

    // A touch device sends the pointer events the logo ignores, then the click.
    pointer(logo, "pointerover", "touch");
    expect(open(logo)).toBe("false");
    const click = new PointerEvent("click", {
      bubbles: true,
      cancelable: true,
      pointerType: "touch",
    });
    act(() => void logo.dispatchEvent(click));
    expect(click.defaultPrevented).toBe(true);
    expect(where()).toBe("/library");
    expect(open(logo)).toBe("true");

    act(() => void vi.advanceTimersByTime(3500));
    expect(open(logo)).toBe("false");
  });

  it("a tap while the name is showing follows the link", () => {
    const logo = setup();
    expect(open(logo)).toBe("true");
    const click = new PointerEvent("click", {
      bubbles: true,
      cancelable: true,
      pointerType: "touch",
    });
    act(() => void logo.dispatchEvent(click));
    expect(where()).toBe("/");
  });

  it("a mouse click on the wound-in logo still navigates", () => {
    const logo = setup();
    act(() => void vi.advanceTimersByTime(3500));
    const click = new PointerEvent("click", {
      bubbles: true,
      cancelable: true,
      pointerType: "mouse",
    });
    act(() => void logo.dispatchEvent(click));
    expect(where()).toBe("/");
  });

  it("stops its timer when it unmounts", () => {
    const { unmount } = render(
      <MemoryRouter>
        <Logo />
      </MemoryRouter>
    );
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("the stylesheet drops the roll animation for reduced motion", () => {
    const css = readFileSync(resolve(process.cwd(), "src/index.css"), "utf8");
    const reduced = css.slice(css.indexOf("@media (prefers-reduced-motion: reduce)"));
    expect(reduced).toMatch(/\.logo-wheel\s*{\s*transition:\s*none/);
    expect(reduced).toMatch(/\.logo-name\s*{\s*transition:\s*none/);
  });
});
