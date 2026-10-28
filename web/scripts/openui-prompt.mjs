#!/usr/bin/env node
// The model's Visualize instruction is OpenUI's own chat prompt, generated from the pinned @openuidev/react-ui chat
// library (native OpenUI): its component signatures and additional rules, without the long worked examples, which
// would double its size on every run. Written to internal/chat/openui_prompt.txt, embedded by the Go server;
// web/src/openui/prompt.test.ts fails when the file is not what this generates.
//
//   node web/scripts/openui-prompt.mjs            write the file
//   node web/scripts/openui-prompt.mjs --print    print it
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
export function generate() {
  const require = createRequire(path.join(here, "..", "package.json"));
  const { openuiChatLibrary, openuiChatPromptOptions } = require("@openuidev/react-ui/genui-lib");
  return openuiChatLibrary.prompt({ additionalRules: openuiChatPromptOptions.additionalRules }).trim() + "\n";
}
export const target = path.join(here, "..", "..", "internal", "chat", "openui_prompt.txt");

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const text = generate();
  if (process.argv.includes("--print")) process.stdout.write(text);
  else fs.writeFileSync(target, text);
}
