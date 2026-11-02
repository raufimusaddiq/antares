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
// Antares' gate (web/src/openui/validate.ts) refuses image addresses and relative links, so the model is told up front.
export const ANTARES_RULES = [
  "Never show images: do not use Image, ImageBlock, ImageGallery, ImageText or ImageTextLarge, leave every src, bgImageSrc and ListItem image empty, and write no Markdown images (![...](...)).",
  "Links in text must be absolute https:// addresses.",
  "Every name you list in a children or items array must have its own line defining that exact name (if you list s5t, write s5t = ...); before you finish, check that each listed name is defined and each defined name is listed.",
  "Write name = only at the start of a line, never inside brackets or arguments: list a part by its name alone ([intro, t1]) or write the component inline without a name ([TextContent(\"...\")]).",
  "Your whole reply is the program, starting with root = Card(: anything you would say before or after it goes in a TextContent inside the Card.",
  "Pick the component that fits the content: EntityList for facts as label and value, Steps for a procedure, Table for comparing items on several attributes, a chart for numbers compared or over time, OverviewCardBlock for two to four headline figures, Tabs for alternatives, TagBlock for keywords; never put more than two TextContent in a row.",
  "Write amounts in Table cells, EntityList rows and metrics as text the reader would write, with currency and digit grouping (\"Rp 300.000\", \"US$12\"); give MetricIndicatorInline a trend only for a real percentage change.",
];
export function generate() {
  const require = createRequire(path.join(here, "..", "package.json"));
  const { openuiChatLibrary, openuiChatPromptOptions } = require("@openuidev/react-ui/genui-lib");
  return openuiChatLibrary.prompt({ additionalRules: [...openuiChatPromptOptions.additionalRules, ...ANTARES_RULES] }).trim() + "\n";
}
export const target = path.join(here, "..", "..", "internal", "chat", "openui_prompt.txt");

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const text = generate();
  if (process.argv.includes("--print")) process.stdout.write(text);
  else fs.writeFileSync(target, text);
}
