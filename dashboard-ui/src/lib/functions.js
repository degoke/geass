/** Runtimes a function can run on; mirrors pkg/platform/functions.go. */
export const FUNCTION_RUNTIMES = [
  { id: "bun", label: "Bun (TypeScript)", file: "index.ts" },
  { id: "node", label: "Node.js", file: "index.mjs" },
  { id: "python", label: "Python", file: "main.py" },
];

export function functionRuntime(id) {
  return FUNCTION_RUNTIMES.find((runtime) => runtime.id === id);
}

export function functionSourceLabel(fn) {
  const runtime = functionRuntime(fn?.runtime);
  return `Function · ${runtime ? runtime.label : fn?.runtime || "unknown runtime"}`;
}
