// Health API (spec §Health API). Polls /fabric/v1/health on demand; observers
// fire when the snapshot changes meaningfully (overall state or peer set).

export class HealthAPI {
  constructor({ transports }) {
    this._transports = transports;
    /** @type {HealthSnapshot|null} */
    this._snapshot = null;
    /** @type {Set<(snapshot: HealthSnapshot) => void>} */
    this._observers = new Set();
  }

  /** Fetch /health from the current transport. */
  async current() {
    const t = this._transports.pick();
    const res = await t.do('GET', '/fabric/v1/health');
    const data = res.data ?? {};
    const overall =
      data.degraded ? 'degraded' :
      (data.event_count === undefined ? 'broken' : 'healthy');
    const snapshot = {
      overall,
      transports: [{
        id: t.id,
        url: t.url,
        reachable: true,
        degradedReasons: data.degraded_reasons ?? [],
        peerHealth: data.peer_health ?? [],
      }],
      degradedReasons: data.degraded_reasons ?? [],
      rawHealth: data,
    };
    this._publish(snapshot);
    return snapshot;
  }

  observe(callback) {
    this._observers.add(callback);
    return () => this._observers.delete(callback);
  }

  _publish(snapshot) {
    const changed =
      !this._snapshot ||
      this._snapshot.overall !== snapshot.overall ||
      JSON.stringify(this._snapshot.degradedReasons) !==
        JSON.stringify(snapshot.degradedReasons);
    this._snapshot = snapshot;
    if (changed) {
      for (const fn of this._observers) {
        try { fn({ ...snapshot }); } catch { /* swallow */ }
      }
    }
  }
}

/** @typedef {{
 *   overall: 'healthy' | 'degraded' | 'broken',
 *   transports: { id: string, url: string, reachable: boolean, degradedReasons: string[], peerHealth: any[] }[],
 *   degradedReasons: string[],
 *   rawHealth: any,
 * }} HealthSnapshot */
