// Small request revision counter used to ignore stale browse responses.
// CommonJS export keeps the same logic directly testable with Node's built-in test runner.
(function (root) {
  function createRequestToken() {
    let revision = 0;
    let controller = null;
    function abortActive() {
      const prev = controller;
      controller = null;
      if (prev && typeof prev.abort === 'function') {
        try { prev.abort(); } catch (e) { /* already aborted */ }
      }
    }
    return {
      begin() {
        abortActive();
        if (typeof AbortController === 'function') controller = new AbortController();
        return ++revision;
      },
      current(token) { return token === revision; },
      signal() { return controller ? controller.signal : undefined; },
      invalidate() {
        abortActive();
        revision++;
      },
    };
  }
  if (typeof module !== 'undefined' && module.exports) module.exports = { createRequestToken };
  root.BrowseRequestToken = createRequestToken;
})(globalThis);
