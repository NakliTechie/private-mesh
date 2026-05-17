// Bundle the SDK into a single ESM file at dist/fabric-sdk.js. The spec
// (fabric-sdk-js-spec-001-v1.1.md §Zero-build embedding) requires a
// browser-usable ESM artifact with no further toolchain.

import { build } from 'esbuild';
import { mkdir, writeFile, stat } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, '..');
const outDir = resolve(repo, 'dist');

await mkdir(outDir, { recursive: true });

const common = {
  bundle: true,
  format: 'esm',
  platform: 'browser',
  target: ['es2022'],
  legalComments: 'none',
  logLevel: 'info',
  banner: {
    js: '/* @naklitechie/fabric-sdk — see docs/specs/fabric-sdk-js-spec-001-v1.1.md */',
  },
  alias: {
    // macaroon@3.0.4 imports node:util for TextEncoder/TextDecoder fallback;
    // browsers have both globally, so we resolve to a tiny stub.
    util: resolve(here, 'util-stub.js'),
  },
};

await build({
  ...common,
  entryPoints: [resolve(repo, 'src/index.js')],
  outfile: resolve(outDir, 'fabric-sdk.js'),
  sourcemap: 'linked',
});

await build({
  ...common,
  entryPoints: [resolve(repo, 'src/index.js')],
  outfile: resolve(outDir, 'fabric-sdk.min.js'),
  minify: true,
  sourcemap: 'linked',
});

// Subresource Integrity hash for the minified artifact.
const minPath = resolve(outDir, 'fabric-sdk.min.js');
const bytes = await import('node:fs').then((m) => m.promises.readFile(minPath));
const sha = createHash('sha384').update(bytes).digest('base64');
await writeFile(resolve(outDir, 'fabric-sdk.min.js.sri'), `sha384-${sha}\n`);

const min = await stat(minPath);
const full = await stat(resolve(outDir, 'fabric-sdk.js'));
console.log(`fabric-sdk.js     ${full.size.toLocaleString()} bytes`);
console.log(`fabric-sdk.min.js ${min.size.toLocaleString()} bytes`);
console.log(`SRI:              sha384-${sha}`);
