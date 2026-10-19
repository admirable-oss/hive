import "@testing-library/jest-dom/vitest";

// jsdom lacks these.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
} as unknown as typeof ResizeObserver;

// jsdom lays nothing out: give the terminal font's probe one cell of 8×17
// per character, and everything else a 960×480 box.
HTMLElement.prototype.getBoundingClientRect = function (this: HTMLElement) {
  const probe = this.classList.contains("cell-probe");
  const width = probe ? 800 : 960;
  const height = probe ? 17 : 480;
  return { width, height, x: 0, y: 0, top: 0, left: 0, right: width, bottom: height, toJSON: () => ({}) } as DOMRect;
};
