// CLI used by scripts/m1-interop.sh.
// Modes:
//   node interop/cli.js generate <interop-dir>
//   node interop/cli.js verify   <interop-dir>
//
// <interop-dir> is the absolute path to interop-tests/m1 in the monorepo.
// Vectors are read from <interop-dir>/../m1-vectors.json.

import { readFile, writeFile, mkdir, stat } from 'node:fs/promises';
import { join, dirname, resolve } from 'node:path';

import { newFIF, parseFIF, newInnerFIF } from '../src/identity/fif.js';
import { mint, parse, verifySignature, alwaysSatisfied } from '../src/grant/macaroon.js';

function die(msg, err) {
  console.error('JS interop:', msg, err ? '\n  ' + (err.stack || err.message || err) : '');
  process.exit(1);
}

function hexToBytes(hex) {
  if (hex.length % 2 !== 0) throw new Error('odd-length hex string');
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.slice(2 * i, 2 * i + 2), 16);
  return out;
}

function bytesToHex(bytes) {
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

async function readVectors(interopDir) {
  const path = resolve(dirname(interopDir), 'm1-vectors.json');
  const text = await readFile(path, 'utf8');
  return JSON.parse(text);
}

async function generateFIF(v, out) {
  const inner = newInnerFIF(
    {
      type: v.fif.principal.type,
      id: v.fif.principal.id,
      display_name: v.fif.principal.display_name,
      created_at: v.fif.principal.created_at,
    },
    {
      algorithm: v.fif.root_keypair.algorithm,
      public_key: hexToBytes(v.fif.root_keypair.public_key_hex),
      private_key: hexToBytes(v.fif.root_keypair.private_key_hex),
    },
  );
  const fif = await newFIF(v.fif.passphrase, inner);
  const bytes = fif.serialize();
  await writeFile(out, bytes);
}

async function verifyFIF(v, inPath) {
  const bytes = new Uint8Array(await readFile(inPath));
  const fif = parseFIF(bytes);
  await fif.unlock(v.fif.passphrase);
  const inner = fif.inner;
  if (inner.principal.id !== v.fif.principal.id) {
    throw new Error(`principal.id mismatch: ${inner.principal.id} vs ${v.fif.principal.id}`);
  }
  if (inner.principal.display_name !== v.fif.principal.display_name) {
    throw new Error(
      `display_name mismatch: ${inner.principal.display_name} vs ${v.fif.principal.display_name}`,
    );
  }
  const gotPub = bytesToHex(inner.root_keypair.public_key);
  if (gotPub !== v.fif.root_keypair.public_key_hex) {
    throw new Error(`root public_key mismatch: ${gotPub}`);
  }
}

async function generateMacaroon(v, out) {
  const rk = hexToBytes(v.macaroon.root_key_hex);
  const id = {
    grant_id: v.macaroon.identifier.grant_id,
    issued_at: v.macaroon.identifier.issued_at,
    issued_by_principal: v.macaroon.identifier.issued_by_principal,
    issued_by_keypair: hexToBytes(v.macaroon.identifier.issued_by_keypair_hex),
    scope: v.macaroon.identifier.scope,
  };
  const g = mint({
    rootKey: rk,
    location: v.macaroon.location,
    identifier: id,
    caveats: v.macaroon.caveats,
  });
  await writeFile(out, g.macaroon);
}

async function verifyMacaroon(v, inPath) {
  const macBytes = new Uint8Array(await readFile(inPath));
  const rk = hexToBytes(v.macaroon.root_key_hex);
  verifySignature(macBytes, rk, alwaysSatisfied);
  const g = parse(macBytes);
  if (g.identifier.grant_id !== v.macaroon.identifier.grant_id) {
    throw new Error(
      `grant_id mismatch: ${g.identifier.grant_id} vs ${v.macaroon.identifier.grant_id}`,
    );
  }
  if (g.identifier.scope.primitive !== v.macaroon.identifier.scope.primitive) {
    throw new Error('primitive mismatch');
  }
  if (g.caveats.length !== v.macaroon.caveats.length) {
    throw new Error(`caveat count: got ${g.caveats.length}, want ${v.macaroon.caveats.length}`);
  }
  for (let i = 0; i < v.macaroon.caveats.length; i++) {
    if (g.caveats[i] !== v.macaroon.caveats[i]) {
      throw new Error(`caveat[${i}]: got ${g.caveats[i]}, want ${v.macaroon.caveats[i]}`);
    }
  }
}

async function pathExists(p) {
  try {
    await stat(p);
    return true;
  } catch {
    return false;
  }
}

async function main() {
  const [mode, dirArg] = process.argv.slice(2);
  if (!mode || !dirArg || (mode !== 'generate' && mode !== 'verify')) {
    console.error('usage: cli.js <generate|verify> <interop-dir>');
    process.exit(2);
  }
  const interopDir = resolve(dirArg);
  const v = await readVectors(interopDir).catch((e) => die('readVectors', e));

  if (mode === 'generate') {
    const outDir = join(interopDir, 'from-js');
    await mkdir(outDir, { recursive: true });
    await generateFIF(v, join(outDir, 'fif.bin')).catch((e) => die('generateFIF', e));
    await generateMacaroon(v, join(outDir, 'macaroon.bin')).catch((e) => die('generateMacaroon', e));
    console.log('JS interop: wrote', outDir);
  } else {
    const inDir = join(interopDir, 'from-go');
    if (!(await pathExists(inDir))) {
      die(`${inDir} not present (run Go generate first)`);
    }
    await verifyFIF(v, join(inDir, 'fif.bin')).catch((e) => die('verifyFIF', e));
    await verifyMacaroon(v, join(inDir, 'macaroon.bin')).catch((e) => die('verifyMacaroon', e));
    console.log('JS interop: verified', inDir);
  }
}

main();
