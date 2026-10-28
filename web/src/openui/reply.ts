import { createContext } from "react";

// The only thing a model-generated component can do: put text into the reader's
// composer. It never sends; the reader reviews and presses Send, so the follow-up
// is an ordinary authenticated message in the current chat (PRD §7.4). No import
// of the OpenUI runtime, so this file is in the main bundle with Chat.
export const MAX_REPLY = 4000; // a follow-up with a filled-in form fits

export const ReplyContext = createContext<((text: string) => void) | null>(null);
