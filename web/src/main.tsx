import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { PwaNotice } from "./PwaNotice";
import "./styles.css";
import { applyAppearance, loadAppearance } from "./appearance";

applyAppearance(loadAppearance()); // before the first paint: no flash of the wrong theme or size

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <PwaNotice />
    <App />
  </StrictMode>,
);
