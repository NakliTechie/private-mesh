// Freshness API (spec §Freshness API). Updates on every protocol response;
// observers fire with the new snapshot.

export class FreshnessAPI {
  constructor({ stalenessBudgetMs = 86_400_000 } = {}) {
    this._budget = stalenessBudgetMs;
    /** @type {FreshnessSnapshot} */
    this._snapshot = {
      asOf: new Date(0),
      peersSynced: [],
      peersMissing: [],
      stalenessMs: 0,
      withinBudget: true,
    };
    /** @type {Set<(snapshot: FreshnessSnapshot) => void>} */
    this._observers = new Set();
  }

  current() {
    return { ...this._snapshot };
  }

  observe(callback) {
    this._observers.add(callback);
    return () => this._observers.delete(callback);
  }

  /** Internal: called by Transport when a response carries freshness. */
  _updateFromEnvelope(envelopeFreshness) {
    if (!envelopeFreshness) return;
    const asOf = envelopeFreshness.as_of ? new Date(envelopeFreshness.as_of) : new Date();
    const stalenessMs = Number(envelopeFreshness.staleness_ms ?? 0);
    this._snapshot = {
      asOf,
      peersSynced: envelopeFreshness.peers_synced ?? [],
      peersMissing: envelopeFreshness.peers_missing ?? [],
      stalenessMs,
      withinBudget: stalenessMs <= this._budget,
    };
    for (const fn of this._observers) {
      try { fn({ ...this._snapshot }); } catch { /* swallow */ }
    }
  }
}

/** @typedef {{
 *   asOf: Date,
 *   peersSynced: any[],
 *   peersMissing: any[],
 *   stalenessMs: number,
 *   withinBudget: boolean,
 * }} FreshnessSnapshot */
