// Bounded request-body reader. The default arrayBuffer() / text() / json()
// APIs in the Workers runtime have no upper bound; an authenticated client
// could OOM the isolate with a multi-GB POST. readBodyCapped() refuses
// requests whose Content-Length already declares oversize, then streams
// the body chunk-by-chunk with a running total to catch missing /
// underreporting Content-Length headers.

export class BodyTooLargeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'BodyTooLargeError';
  }
}

export async function readBodyCapped(req: Request, maxBytes: number): Promise<Uint8Array> {
  const cl = req.headers.get('content-length');
  if (cl !== null) {
    const n = Number(cl);
    if (Number.isFinite(n) && n > maxBytes) {
      throw new BodyTooLargeError(`content-length ${n} exceeds limit ${maxBytes}`);
    }
  }
  if (!req.body) return new Uint8Array(0);
  const reader = req.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > maxBytes) {
      try {
        await reader.cancel();
      } catch {
        // ignore — already failing the request
      }
      throw new BodyTooLargeError(`body exceeds limit ${maxBytes}`);
    }
    chunks.push(value);
  }
  const out = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) {
    out.set(c, off);
    off += c.byteLength;
  }
  return out;
}

// requestBodyLimitBytes returns the body cap from env (paralleling the Hub:
// 2x MaxEventSizeBytes + 256 KiB header slop).
export function requestBodyLimitBytes(env: { MAX_EVENT_SIZE_BYTES?: string }): number {
  const HEADER_SLOP = 256 << 10;
  const eventMax = parseInt(env.MAX_EVENT_SIZE_BYTES ?? '', 10);
  const safeMax = Number.isFinite(eventMax) && eventMax > 0 ? eventMax : 1 << 20;
  return safeMax * 2 + HEADER_SLOP;
}
