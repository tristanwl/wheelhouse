import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";
import { resetApiState } from "./lib/http";

afterEach(() => {
  cleanup();
  resetApiState();
});

// jsdom has no layout; pages scroll to the top on navigation.
window.scrollTo = () => {};
