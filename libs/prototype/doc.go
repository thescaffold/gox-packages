// Package prototype makes the clickable prototype of a system from its design (TRD §6.20, PLAN M2-08).
//
// The prototype is a static site: HTML, CSS and classic JavaScript with made-up data and no server. That is what
// lets it be shared by link, opened from a folder, hosted anywhere and shown to people who do not use Origine.
// Build makes one deterministically (the same design and options give the same bytes), and Check holds any
// prototype, however it was made, to the rules that make it safe to share:
//
//   - the entry is index.html, with relative addresses only, hash routing and classic scripts (no modules);
//   - nothing is loaded from, or sent to, the network: no remote scripts, styles, images or fonts, and none of
//     fetch, XMLHttpRequest, WebSocket, EventSource, sendBeacon, workers or service workers;
//   - no eval, no new Function, no document.write;
//   - forms do not submit anywhere;
//   - manifest.json names the revision and the SHA-256 of every other file, and every hash is right.
package prototype
