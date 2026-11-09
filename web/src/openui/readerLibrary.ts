import "./zodConfig"; // must stay first: see that file
import { createElement } from "react";
import { createLibrary, defineComponent } from "@openuidev/react-lang";
import { openuiChatLibrary } from "@openuidev/react-ui/genui-lib";

// The read-only OpenUI library for published posts (specs T061). A reader can sort, expand and hover; a reader can not act. So
// this library holds only components that show: content, tables, charts, lists, sections and tags. Not in it, and therefore
// unknown to the parser (the block falls back to its plain text): forms and every input, buttons and icon buttons, follow-ups,
// images, carousels and the card blocks. No action step exists either, so even a program that somehow named one has nothing to
// run. internal/blog/openui.go (readerComponents) is the server's copy of this list; a test on each side keeps them equal.
export const READER_COMPONENTS = [
  "Card", "CardHeader", "TextContent", "MarkDownRenderer", "Callout", "TextCallout", "CodeBlock", "Separator", "InlineHeader",
  "Table", "Col",
  "BarChart", "LineChart", "AreaChart", "RadarChart", "HorizontalBarChart", "Series",
  "PieChart", "RadialChart", "SingleStackedBarChart", "Slice",
  "ScatterChart", "ScatterSeries", "Point",
  "ListBlock", "ListItem", "SectionBlock", "SectionItem", "Tabs", "TabItem", "Accordion", "AccordionItem", "Steps", "StepsItem",
  "TagBlock", "Tag", "EntityList", "Icon",
] as const;

const chat = openuiChatLibrary.components as Record<string, Parameters<typeof createLibrary>[0]["components"][number]>;

// The chat Card draws its sources as a strip whose icons are fetched from a third-party favicon service: a request from the
// reader's browser to another site for every source domain. A reader page makes no such request (and the page's policy would
// refuse it), so the Card is the chat's own with that strip taken off. The post lists each block's sources itself, from the
// ones the server validated (DocView's SourcesLine), so nothing is lost and nothing is listed twice.
const ChatCard = chat.Card as unknown as {
  props: Parameters<typeof defineComponent>[0]["props"];
  description: string;
  component: React.ComponentType<{ props: Record<string, unknown>; renderNode: (v: unknown) => React.ReactNode }>;
};
const ReaderCard = defineComponent({
  name: "Card",
  props: ChatCard.props,
  description: ChatCard.description,
  component: ({ props, renderNode }) => createElement(ChatCard.component, { props: { ...props, sources: undefined }, renderNode }),
});

export const readerLibrary = createLibrary({
  components: READER_COMPONENTS.map((n) => {
    if (n === "Card") return ReaderCard;
    const c = chat[n];
    if (!c) throw new Error(`the chat library has no ${n}: the reader list is out of step with @openuidev/react-ui`);
    return c;
  }),
  root: "Card",
});
