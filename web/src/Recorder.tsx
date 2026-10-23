import { useEffect, useRef, useState } from "react";

// Voice capture (PRD §21.4, UXP-004). Dictation is not offered: the backend has no
// speech-to-text route to use (docs/runbooks/voice.md) and Antares will not call a provider itself. What works is recording
// and checking a take on this device. Nothing is uploaded and nothing reaches the composer.
export const MAX_SECONDS = 300;

const TYPES = ["audio/webm;codecs=opus", "audio/webm", "audio/ogg;codecs=opus", "audio/mp4"];

type Phase = { name: "idle" } | { name: "recording"; seconds: number } | { name: "ready"; url: string } | { name: "error"; message: string };

function failure(e: unknown): string {
  const name = e instanceof DOMException ? e.name : "";
  if (name === "NotAllowedError" || name === "SecurityError") return "Microphone access was refused. Allow it in the browser's site settings to record.";
  if (name === "NotFoundError") return "No microphone was found on this device.";
  return "The microphone could not be started.";
}

export function Recorder() {
  const [phase, setPhase] = useState<Phase>({ name: "idle" });
  const rec = useRef<MediaRecorder | null>(null);
  const supported = typeof MediaRecorder !== "undefined" && !!navigator.mediaDevices?.getUserMedia;

  useEffect(
    () => () => {
      if (rec.current && rec.current.state !== "inactive") {
        rec.current.onstop = null;
        rec.current.stop();
      }
      rec.current?.stream.getTracks().forEach((t) => t.stop());
    },
    [],
  );
  useEffect(() => {
    if (phase.name !== "ready") return;
    return () => URL.revokeObjectURL(phase.url);
  }, [phase]);

  useEffect(() => {
    if (phase.name !== "recording") return;
    const t = setInterval(() => {
      setPhase((p) => (p.name === "recording" ? { name: "recording", seconds: p.seconds + 1 } : p));
    }, 1000);
    return () => clearInterval(t);
  }, [phase.name]);
  useEffect(() => {
    if (phase.name === "recording" && phase.seconds >= MAX_SECONDS) rec.current?.stop();
  }, [phase]);

  if (!supported) {
    return <p className="recorder-note">Voice recording is not available in this browser.</p>;
  }

  const start = async () => {
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const type = TYPES.find((t) => MediaRecorder.isTypeSupported(t));
      const r = new MediaRecorder(stream, type ? { mimeType: type } : undefined);
      const chunks: Blob[] = [];
      r.ondataavailable = (e) => e.data.size > 0 && chunks.push(e.data);
      r.onstop = () => {
        stream.getTracks().forEach((t) => t.stop());
        setPhase({ name: "ready", url: URL.createObjectURL(new Blob(chunks, { type: r.mimeType })) });
      };
      rec.current = r;
      r.start();
      setPhase({ name: "recording", seconds: 0 });
    } catch (e) {
      setPhase({ name: "error", message: failure(e) });
    }
  };
  const clock = (s: number) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;

  return (
    <div className="recorder">
      {phase.name === "recording" ? (
        <>
          <span role="timer" aria-label="Recording time">
            {clock(phase.seconds)} / {clock(MAX_SECONDS)}
          </span>
          <button type="button" onClick={() => rec.current?.stop()}>
            Stop
          </button>
        </>
      ) : (
        <button type="button" onClick={() => void start()}>
          {phase.name === "ready" ? "Record again" : "Record"}
        </button>
      )}
      {phase.name === "ready" && (
        <>
          <audio controls src={phase.url} aria-label="Your recording" />
          <button type="button" className="quiet" onClick={() => setPhase({ name: "idle" })}>
            Discard
          </button>
        </>
      )}
      {phase.name === "error" && <span role="alert">{phase.message}</span>}
      <span className="recorder-note">Stays on this device. Speech-to-text is unavailable: no transcription service is connected.</span>
    </div>
  );
}
