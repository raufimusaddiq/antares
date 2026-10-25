// The ONE browser message the image check tolerates (#139): the attached-image thumbnail is a blob: URL and the page's CSP is
// img-src 'self', so the browser blocks it. This is a pre-existing finding, not caused by the visual refactor; production CSP is
// unchanged in that PR. Browsers word the same console line differently, and the CI-pinned Chromium (138) differs from a current one:
//   current:  Loading the image 'blob:ORIGIN/ID' violates the following Content Security Policy directive: "img-src 'self'". The action has been blocked.
//   138:      Refused to load the image 'blob:ORIGIN/ID' because it violates the following Content Security Policy directive: "img-src 'self'".\n   (Chromium 138 ends this console line with a newline; observed on the CI-pinned chrome-headless-shell 138.0.7204.94.)
// Both are matched exactly: the blob URL must belong to the page's own origin and the directive must be img-src 'self'. The structured
// securitypolicyviolation event (violatedDirective + " " + blockedURI) is matched the same way. Nothing else is tolerated: a blob: image
// blocked by any other directive, a non-blob image, another resource type or origin, a script, a style, a frame all stay failures.
const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
const ID = "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}";

export function knownBlobConsole(message, origin) {
  const blob = `'blob:${esc(origin)}/${ID}'`;
  const dir = `"img-src 'self'"`;
  return (
    new RegExp(`^Loading the image ${blob} violates the following Content Security Policy directive: ${dir}\\. The action has been blocked\\.\\n?$`).test(message) ||
    new RegExp(`^Refused to load the image ${blob} because it violates the following Content Security Policy directive: ${dir}\\.\\n?$`).test(message)
  );
}

// document "securitypolicyviolation" listener output: `${violatedDirective} ${blockedURI}`; a blob: URI is reported as "blob" (current) or
// the full blob URL (138 reports the URL); the directive is img-src or its element form.
export function knownBlobEvent(entry, origin) {
  return new RegExp(`^img-src(-elem)? (blob|blob:${esc(origin)}/${ID})$`).test(entry);
}

// The deliberate failed upload of the image flow answers 415; the browser logs it as a failed resource load.
export const deliberate415 = (message) => /^Failed to load resource: the server responded with a status of 415 \(Unsupported Media Type\)$/.test(message);

/** What is left after removing the tolerated messages. Empty means clean. */
export function unexpectedMessages(consoleErrors, cspEvents, origin) {
  return [...consoleErrors.filter((m) => !knownBlobConsole(m, origin) && !deliberate415(m)), ...cspEvents.filter((e) => !knownBlobEvent(e, origin))];
}
