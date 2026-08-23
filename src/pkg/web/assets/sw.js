// A service worker, because a page cannot be installed without one — and it is installation that
// puts drop in Android's share sheet.
//
// It deliberately caches nothing. Everything here is served from the device you are talking to, and
// a stale conversation would be worse than a slow one. This exists to make the page installable and
// to get out of the way.

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));
self.addEventListener("fetch", () => {});
