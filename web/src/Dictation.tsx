import { useEffect, useRef, useState } from "react";

// Dictation (#167, PRD §21.4, UXP-004). The assistant has no speech-to-text route (docs/runbooks/voice.md), and
// Antares calls no speech provider itself. Where the browser has its own speech recognition, the user can choose to
// use it, after a disclosure of where the audio goes: Chrome and Edge send it to their maker's speech service; Antares
// never receives it. The text lands in the message box for editing and is never sent on its own. Without browser
// support the control is not shown, and a note says why.

const CONSENT_KEY = "antares:dictation-consent:v1";

interface Recognition {
  lang: string;
  continuous: boolean;
  interimResults: boolean;
  onresult: ((e: { resultIndex: number; results: ArrayLike<{ isFinal: boolean; 0: { transcript: string } }> }) => void) | null;
  onerror: ((e: { error: string }) => void) | null;
  onend: (() => void) | null;
  start(): void;
  stop(): void;
  abort(): void;
}
type RecognitionCtor = new () => Recognition;

export function recognitionCtor(w: unknown = typeof window === "undefined" ? undefined : window): RecognitionCtor | null {
  const o = w as { SpeechRecognition?: RecognitionCtor; webkitSpeechRecognition?: RecognitionCtor } | undefined;
  return o?.SpeechRecognition ?? o?.webkitSpeechRecognition ?? null;
}

function failure(code: string): string {
  switch (code) {
    case "not-allowed":
    case "service-not-allowed":
      return "Microphone or speech service access was refused. Allow it in the browser's site settings to dictate.";
    case "no-speech":
      return "No speech was heard. Nothing was added.";
    case "audio-capture":
      return "No microphone was found on this device.";
    case "network":
      return "The browser's speech service could not be reached. Nothing was added.";
    case "aborted":
      return "";
    default:
      return "Dictation stopped. Nothing more was added.";
  }
}

const consented = () => {
  try {
    return localStorage.getItem(CONSENT_KEY) === "yes";
  } catch {
    return false;
  }
};

/** onText receives each finished phrase; the caller adds it to the editable draft. */
export function Dictation({ onText }: { onText: (text: string) => void }) {
  const Ctor = recognitionCtor();
  const [listening, setListening] = useState(false);
  const [asking, setAsking] = useState(false);
  const [error, setError] = useState("");
  const rec = useRef<Recognition | null>(null);
  const deliver = useRef(onText);
  deliver.current = onText;

  useEffect(() => () => rec.current?.abort(), []);

  if (!Ctor) {
    return <span className="recorder-note">Dictation is not available in this browser, and the assistant has no speech-to-text service.</span>;
  }

  const start = () => {
    setError("");
    const r = new Ctor();
    r.lang = navigator.language || "en-US";
    r.continuous = true;
    r.interimResults = false;
    r.onresult = (e) => {
      const parts: string[] = [];
      for (let i = e.resultIndex; i < e.results.length; i++) {
        const res = e.results[i];
        if (res.isFinal && res[0].transcript.trim()) parts.push(res[0].transcript.trim());
      }
      if (parts.length) deliver.current(parts.join(" ")); // one delivery per event: the draft updates once
    };
    r.onerror = (e) => setError(failure(e.error));
    r.onend = () => {
      setListening(false);
      rec.current = null;
    };
    rec.current = r;
    try {
      r.start();
      setListening(true);
    } catch {
      rec.current = null;
      setError("Dictation could not start.");
    }
  };

  return (
    <div className="dictation">
      {listening ? (
        <button type="button" onClick={() => rec.current?.stop()}>
          Stop dictation
        </button>
      ) : (
        <button type="button" data-icon="mic" onClick={() => (consented() ? start() : setAsking(true))}>
          Dictate
        </button>
      )}
      {asking && (
        <div role="dialog" aria-label="About dictation" className="notice">
          <p>
            Dictation uses your browser's own speech service. In Chrome and Edge your voice is sent to the browser's maker (Google or Microsoft) to be turned into text; Antares never receives the audio. The text appears in the message box for you to check and edit; nothing is sent until you press Send.
          </p>
          <button
            type="button"
            onClick={() => {
              try {
                localStorage.setItem(CONSENT_KEY, "yes");
              } catch {
                // not remembered: asked again next time
              }
              setAsking(false);
              start();
            }}
          >
            Use dictation
          </button>{" "}
          <button type="button" className="quiet" onClick={() => setAsking(false)}>
            Not now
          </button>
        </div>
      )}
      {listening && <span role="status">Listening… speak, then press Stop.</span>}
      {error && <span role="alert">{error}</span>}
    </div>
  );
}
