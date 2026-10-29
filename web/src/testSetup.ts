import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Unmount whatever a test rendered (a no-op in node-environment files).
afterEach(() => cleanup());
