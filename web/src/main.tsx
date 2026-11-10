import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { PwaNotice } from "./PwaNotice";
import { SharedChat, sharedToken } from "./SharedChat";
import { PublicIndex, PublicPostPage, publicBlogRoute } from "./blog/Public";
import "./styles.css";
import { applyAppearance, loadAppearance } from "./appearance";

applyAppearance(loadAppearance()); // before the first paint: no flash of the wrong theme or size

// A shared chat's address is the read-only page alone: no session is loaded and nothing of the app mounts.
const shared = sharedToken(window.location.pathname);
// The public blog (/b, /b/<slug>) is read the same way: no session is loaded and nothing of the app mounts. The one question it
// asks is who is reading (a single GET /api/me, so a signed-in reader sees "Back to Antares"); that call writes nothing.
const blog = publicBlogRoute(window.location.pathname);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    {shared ? (
      <SharedChat token={shared} />
    ) : blog ? (
      blog.kind === "index" ? <PublicIndex /> : <PublicPostPage slug={blog.slug} />
    ) : (
      <>
        <PwaNotice />
        <App />
      </>
    )}
  </StrictMode>,
);
