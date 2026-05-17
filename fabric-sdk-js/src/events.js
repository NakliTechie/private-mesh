// Minimal event bus used by Fabric to surface async events (conflicts,
// degradation, agent retirement, …). Spec: §Event bus.

export class EventBus {
  constructor() {
    /** @type {Map<string, Set<(payload: any) => void>>} */
    this._handlers = new Map();
  }

  /**
   * Register a handler. Returns an unsubscribe function.
   *
   * @param {string} type
   * @param {(payload: any) => void} handler
   */
  on(type, handler) {
    if (!this._handlers.has(type)) this._handlers.set(type, new Set());
    this._handlers.get(type).add(handler);
    return () => this.off(type, handler);
  }

  off(type, handler) {
    const set = this._handlers.get(type);
    if (set) set.delete(handler);
  }

  emit(type, payload) {
    const set = this._handlers.get(type);
    if (!set) return;
    for (const h of set) {
      try {
        h(payload);
      } catch (e) {
        // Listeners must not break the bus.
        // eslint-disable-next-line no-console
        console.warn('fabric event handler threw', { type, error: e });
      }
    }
  }
}
