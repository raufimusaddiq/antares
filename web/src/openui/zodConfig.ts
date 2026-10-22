import { z } from "zod/v4";

// Zod compiles validators with `new Function` unless told not to, and probes for
// that ability first. Under our CSP (no 'unsafe-eval') the probe is refused and
// reported as a violation even though it is caught. Turning the compiler off
// skips the probe; the schemas here are tiny and validate the same, interpreted.
//
// Import this module FIRST, before anything that imports @openuidev/react-lang:
// the setting must be in place before the first object schema is created.
z.config({ jitless: true });
