// Tiny static server that exposes the SDK's dist/ and browser-test/pages/
// to the browser under test. Used by scripts/js-gate.sh.
//
// Layout:
//   /dist/*           → dist/<file>
//   /sandbox.html     → browser-test/pages/sandbox.html
//   /                 → 404
//
// Port is the first CLI arg.

import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { extname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { dirname } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, '..');
const port = Number(process.argv[2] ?? 5172);

const mime = {
  '.js':   'text/javascript; charset=utf-8',
  '.mjs':  'text/javascript; charset=utf-8',
  '.map':  'application/json',
  '.html': 'text/html; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.css':  'text/css; charset=utf-8',
  '.wasm': 'application/wasm',
};

function fileFor(url) {
  if (url === '/' || url === '/sandbox.html') {
    return resolve(repo, 'browser-test/pages/sandbox.html');
  }
  if (url.startsWith('/dist/')) {
    return resolve(repo, 'dist', url.slice('/dist/'.length));
  }
  if (url.startsWith('/pages/')) {
    return resolve(repo, 'browser-test/pages', url.slice('/pages/'.length));
  }
  return null;
}

const server = createServer(async (req, res) => {
  const u = new URL(req.url, `http://localhost:${port}`);
  const path = fileFor(u.pathname);
  if (!path) {
    res.writeHead(404).end('not found');
    return;
  }
  try {
    const body = await readFile(path);
    res.writeHead(200, {
      'Content-Type': mime[extname(path)] ?? 'application/octet-stream',
      'Cache-Control': 'no-store',
      // Permissive so localhost:HUB_PORT is reachable from the page.
      'Access-Control-Allow-Origin': '*',
    });
    res.end(body);
  } catch {
    res.writeHead(404).end('not found');
  }
});

server.listen(port, '127.0.0.1', () => {
  console.log(`serve-test: listening on http://127.0.0.1:${port}`);
});
