// Small request revision counter used to ignore stale browse responses.
// CommonJS export keeps the same logic directly testable with Node's built-in test runner.
(function (root) {
  function createRequestToken() {
    let revision = 0;
    return {
      begin() { return ++revision; },
      current(token) { return token === revision; },
      invalidate() { revision++; },
    };
  }
  if (typeof module !== 'undefined' && module.exports) module.exports = { createRequestToken };
  root.BrowseRequestToken = createRequestToken;
})(globalThis);
