const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/**
 * "8 Nov 2026" from the date part of an ISO timestamp, as the design shows dates (or "8 Nov" without the year).
 * It reads the digits rather than building a Date, so the day does not move with the viewer's time zone; anything
 * that is not an ISO date is shown as it came.
 */
export function formatDay(iso: string, withYear = true): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(iso);
  const month = m ? MONTHS[Number(m[2]) - 1] : undefined;
  if (!m || !month) return iso;
  return `${Number(m[3])} ${month}${withYear ? ` ${m[1]}` : ""}`;
}
