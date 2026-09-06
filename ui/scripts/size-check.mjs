#!/usr/bin/env node
// Bundle budget gate, run at the end of `npm run build` (also available as
// `npm run size` on an existing build). Hard budget from the team: the
// initial route chunk (what index.html loads eagerly -- the app shell, not
// any lazy-loaded page) must gzip under 120 KB, and the whole built app
// under 300 KB gzipped. Exits non-zero when either is exceeded so CI (or a
// developer) can't miss it.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { gzipSync } from 'node:zlib';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const distDir = path.resolve(__dirname, '..', '..', 'internal', 'ui', 'dist');

const INITIAL_BUDGET_BYTES = 120 * 1024;
const TOTAL_BUDGET_BYTES = 300 * 1024;

function walk(dir) {
  let out = [];
  for (const entry of readdirSync(dir)) {
    const full = path.join(dir, entry);
    const st = statSync(full);
    if (st.isDirectory()) out = out.concat(walk(full));
    else out.push(full);
  }
  return out;
}

function gzipSize(file) {
  return gzipSync(readFileSync(file)).length;
}

function fmtKB(bytes) {
  return `${(bytes / 1024).toFixed(1)} KB`;
}

function main() {
  let indexHtml;
  try {
    indexHtml = readFileSync(path.join(distDir, 'index.html'), 'utf8');
  } catch {
    console.error(`size-check: no build found at ${distDir} (run "vite build" first)`);
    process.exit(1);
  }

  // Files index.html loads eagerly: its own <script type="module"> entry
  // plus any <link rel="modulepreload">. Vite does not modulepreload chunks
  // that are only reachable through a dynamic import() (React.lazy), so
  // this set is exactly the "initial route" bundle.
  const eagerPaths = new Set();
  for (const m of indexHtml.matchAll(/<script[^>]+type="module"[^>]+src="([^"]+)"/g)) eagerPaths.add(m[1]);
  for (const m of indexHtml.matchAll(/<link[^>]+rel="modulepreload"[^>]+href="([^"]+)"/g)) eagerPaths.add(m[1]);
  // The stylesheet <link> loads unconditionally on every page view too (it's
  // not behind a dynamic import like a lazy page chunk is), so it counts
  // toward the initial bundle even though it isn't a <script>/modulepreload.
  for (const m of indexHtml.matchAll(/<link[^>]+rel="stylesheet"[^>]+href="([^"]+)"/g)) eagerPaths.add(m[1]);

  const allFiles = walk(distDir).filter((f) => /\.(js|css)$/.test(f));
  const rows = allFiles
    .map((f) => {
      const rel = '/ui/' + path.relative(distDir, f).split(path.sep).join('/');
      return { file: path.relative(distDir, f), gzip: gzipSize(f), raw: statSync(f).size, eager: eagerPaths.has(rel) };
    })
    .sort((a, b) => b.gzip - a.gzip);

  console.log('Per-chunk sizes (gzipped):');
  for (const r of rows) {
    console.log(`  ${r.eager ? '[initial]' : '[lazy]   '} ${r.file.padEnd(40)} ${fmtKB(r.gzip).padStart(9)} (raw ${fmtKB(r.raw)})`);
  }

  const initialBytes = rows.filter((r) => r.eager).reduce((s, r) => s + r.gzip, 0);
  const totalBytes = rows.reduce((s, r) => s + r.gzip, 0);

  console.log('');
  console.log(`Initial route chunk: ${fmtKB(initialBytes)} (budget ${fmtKB(INITIAL_BUDGET_BYTES)})`);
  console.log(`Whole app:           ${fmtKB(totalBytes)} (budget ${fmtKB(TOTAL_BUDGET_BYTES)})`);

  let failed = false;
  if (initialBytes > INITIAL_BUDGET_BYTES) {
    console.error(`FAIL: initial route chunk ${fmtKB(initialBytes)} exceeds ${fmtKB(INITIAL_BUDGET_BYTES)} budget`);
    failed = true;
  }
  if (totalBytes > TOTAL_BUDGET_BYTES) {
    console.error(`FAIL: whole app ${fmtKB(totalBytes)} exceeds ${fmtKB(TOTAL_BUDGET_BYTES)} budget`);
    failed = true;
  }
  if (failed) process.exit(1);
  console.log('OK: within budget.');
}

main();
