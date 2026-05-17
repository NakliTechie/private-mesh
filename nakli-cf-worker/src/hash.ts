// Hashing helpers. The Workers runtime has crypto.subtle for SHA-256.

export async function sha256Hex(b: Uint8Array): Promise<string> {
  const h = await crypto.subtle.digest('SHA-256', b);
  return [...new Uint8Array(h)].map((x) => x.toString(16).padStart(2, '0')).join('');
}

export async function sha256Bytes(b: Uint8Array): Promise<Uint8Array> {
  const h = await crypto.subtle.digest('SHA-256', b);
  return new Uint8Array(h);
}
