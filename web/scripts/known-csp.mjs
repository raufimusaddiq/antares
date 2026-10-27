// What the image check of scripts/browser-check.mjs tolerates. Since #142 the page's CSP is img-src 'self' blob:, so the
// attached-image thumbnail (a blob: URL the page made itself) renders and no CSP message is excused any more. The only
// tolerated line is the deliberate failed upload of that flow, which answers 415 and is logged as a failed resource load.
export const deliberate415 = (message) => /^Failed to load resource: the server responded with a status of 415 \(Unsupported Media Type\)$/.test(message);

/** What is left after removing the tolerated messages. Empty means clean: every CSP violation is unexpected. */
export function unexpectedMessages(consoleErrors, cspEvents) {
  return [...consoleErrors.filter((m) => !deliberate415(m)), ...cspEvents];
}
