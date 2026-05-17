// Browser-only stub for Node's `util` module. The macaroon npm package
// imports `util` solely for `TextEncoder`/`TextDecoder` when running in Node;
// browsers have those on globalThis, so we just re-export from there.
export const TextEncoder = globalThis.TextEncoder;
export const TextDecoder = globalThis.TextDecoder;
export default { TextEncoder, TextDecoder };
