// What Mantine asks of a browser and jsdom does not have.
import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

afterEach(() => cleanup());

window.matchMedia ||= (query) => ({
  matches: false,
  media: query,
  onchange: null,
  addListener() {},
  removeListener() {},
  addEventListener() {},
  removeEventListener() {},
  dispatchEvent: () => false,
});

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ||= ResizeObserverStub;
window.scrollTo = () => {};

// xterm draws on a canvas that jsdom does not implement.
HTMLCanvasElement.prototype.getContext = () => null;
