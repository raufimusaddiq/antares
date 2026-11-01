import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { PwaNotice } from "./PwaNotice";
import { SharedChat, sharedToken } from "./SharedChat";
import "./styles.css";
import { applyAppearance, loadAppearance } from "./appearance";

applyAppearance(loadAppearance()); // before the first paint: no flash of the wrong theme or size

// A shared chat's address is the read-only page alone: no session is loaded and nothing of the app mounts.
const shared = sharedToken(window.location.pathname);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    {shared ? (
      <SharedChat token={shared} />
    ) : (
      <>
        <PwaNotice />
        <App />
      </>
    )}
  </StrictMode>,
);
