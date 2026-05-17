declare module 'macaroon' {
  export interface ExportedMacaroon {
    i?: string;
    i64?: string;
    c?: Array<{ i?: string; i64?: string; l?: string; vid?: string }>;
  }

  export interface Macaroon {
    addFirstPartyCaveat(condition: Uint8Array): void;
    exportBinary(): Uint8Array;
    exportJSON(): ExportedMacaroon;
    verify(
      rootKey: Uint8Array,
      check: (condition: string) => unknown,
      discharges: Macaroon[],
    ): void;
  }

  export interface NewMacaroonOptions {
    identifier: Uint8Array;
    location: string;
    rootKey: Uint8Array;
    version: number;
  }

  export function newMacaroon(opts: NewMacaroonOptions): Macaroon;
  export function importMacaroon(bytes: Uint8Array): Macaroon;

  const defaultExport: {
    newMacaroon: typeof newMacaroon;
    importMacaroon: typeof importMacaroon;
  };
  export default defaultExport;
}
