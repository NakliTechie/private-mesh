import { describe, it, expect } from 'vitest';
import { evaluateCaveats, CaveatError, CaveatContext } from '../src/caveats.js';

// Build a minimal CaveatContext for unit tests of the binding caveats.
// rateConsume/dischargeLookup never fire in these scenarios.
function baseCtx(over: Partial<CaveatContext> = {}): CaveatContext {
  return {
    now: new Date(),
    operation: 'read',
    namespace: '',
    primitive: 'vault',
    hasIdempotencyKey: false,
    isDelegationRequest: false,
    grantId: 'gid',
    isBridgeCall: false,
    rateConsume: () => true,
    dischargeIds: new Set(),
    dischargeLookup: () => false,
    ...over,
  };
}

describe('caveat binding — strict vs lax (regression for header-omission bypass)', () => {
  const cases: Array<{
    name: string;
    caveat: string;
    matchValue: string;
    setRequester: (over: Partial<CaveatContext>, v: string) => void;
    headerName: string;
  }> = [
    {
      name: 'agent-id ==',
      caveat: 'agent-id == 01JAGENTSAMPLE0000000001',
      matchValue: '01JAGENTSAMPLE0000000001',
      setRequester: (o, v) => { o.requesterAgentId = v; },
      headerName: 'X-Fabric-Agent-Id',
    },
    {
      name: 'device-id ==',
      caveat: 'device-id == 01JDEVICESAMPLE000000001',
      matchValue: '01JDEVICESAMPLE000000001',
      setRequester: (o, v) => { o.requesterDeviceId = v; },
      headerName: 'X-Fabric-Device-Id',
    },
    {
      name: 'principal-type in [human]',
      caveat: 'principal-type in [human]',
      matchValue: 'human',
      setRequester: (o, v) => { o.requesterPrincipalType = v; },
      headerName: 'X-Fabric-Principal-Type',
    },
  ];

  for (const tc of cases) {
    describe(tc.name, () => {
      it('lax + absent header → passes (compat — the bypass behavior, intentional default)', () => {
        const ctx = baseCtx();
        expect(() => evaluateCaveats([tc.caveat], ctx)).not.toThrow();
      });

      it('lax + matching header → passes', () => {
        const over: Partial<CaveatContext> = {};
        tc.setRequester(over, tc.matchValue);
        expect(() => evaluateCaveats([tc.caveat], baseCtx(over))).not.toThrow();
      });

      it('lax + mismatched header → fails', () => {
        const over: Partial<CaveatContext> = {};
        tc.setRequester(over, 'wrong-value');
        expect(() => evaluateCaveats([tc.caveat], baseCtx(over))).toThrow(CaveatError);
      });

      it('strict + absent header → FAILS (the bypass fix)', () => {
        const ctx = baseCtx({ strictBinding: true });
        try {
          evaluateCaveats([tc.caveat], ctx);
          throw new Error('expected CaveatError, got success — the bypass is back');
        } catch (e) {
          expect(e).toBeInstanceOf(CaveatError);
          expect((e as CaveatError).reason).toContain(tc.headerName);
        }
      });

      it('strict + matching header → passes', () => {
        const over: Partial<CaveatContext> = { strictBinding: true };
        tc.setRequester(over, tc.matchValue);
        expect(() => evaluateCaveats([tc.caveat], baseCtx(over))).not.toThrow();
      });

      it('strict + mismatched header → fails', () => {
        const over: Partial<CaveatContext> = { strictBinding: true };
        tc.setRequester(over, 'wrong-value');
        expect(() => evaluateCaveats([tc.caveat], baseCtx(over))).toThrow(CaveatError);
      });
    });
  }

  it('default flag (strictBinding omitted) preserves prior bypass behavior', () => {
    // Belt and suspenders: an undefined strictBinding must behave exactly
    // like strictBinding=false so this change is safe to ship with current
    // consumers that don't yet send the binding headers.
    for (const c of cases) {
      expect(() => evaluateCaveats([c.caveat], baseCtx())).not.toThrow();
    }
  });
});
