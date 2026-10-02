// Loads spec.wasm in Node, analyzes every text in the inputs file and
// writes {name: analysisJSON} plus the load and per-call timings (to stderr).
// Usage: node run.js <wasm_exec.js> <spec.wasm> <inputs.json> <outputs.json>   (inputs: {name: text})
const fs = require("fs");
const [execJs, wasmPath, inputs, outputs] = process.argv.slice(2);
const texts = JSON.parse(fs.readFileSync(inputs, "utf8"));
globalThis.require = require;
globalThis.fs = fs;
globalThis.TextEncoder = TextEncoder;
globalThis.TextDecoder = TextDecoder;
globalThis.performance ??= require("perf_hooks").performance;
globalThis.crypto ??= require("crypto").webcrypto;
require(execJs);
(async () => {
  const go = new Go();
  const t0 = performance.now();
  const { instance } = await WebAssembly.instantiate(fs.readFileSync(wasmPath), go.importObject);
  go.run(instance);
  const loaded = performance.now() - t0;
  const out = {};
  const times = [];
  for (const [name, text] of Object.entries(texts)) {
    const t = performance.now();
    out[name] = ospecAnalyze(text);
    times.push(performance.now() - t);
    if (name === "big") console.error(JSON.stringify({ bigBytes: text.length, bigMs: +(performance.now() - t).toFixed(1) }));
  }
  times.sort((a, b) => a - b);
  console.error(JSON.stringify({ loadMs: +loaded.toFixed(1), calls: times.length, p50: +times[times.length >> 1].toFixed(2), max: +times[times.length - 1].toFixed(2) }));
  fs.writeFileSync(outputs, JSON.stringify(out));
  process.exit(0);
})();
