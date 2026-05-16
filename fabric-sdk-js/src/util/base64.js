// Standard padded base64 helpers. Matches Go's encoding/json default for
// []byte fields, so JSON produced by either SDK round-trips.

/** Encode bytes as standard-padded base64. */
export function bytesToBase64(bytes) {
  if (typeof Buffer !== 'undefined') {
    return Buffer.from(bytes).toString('base64');
  }
  let s = '';
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return globalThis.btoa(s);
}

/** Decode standard-padded base64 to a Uint8Array. */
export function base64ToBytes(b64) {
  if (typeof Buffer !== 'undefined') {
    return new Uint8Array(Buffer.from(b64, 'base64'));
  }
  const bin = globalThis.atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}
