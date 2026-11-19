import { lazy, Suspense, type ComponentProps } from "react";

// Stand-in for react-syntax-highlighter's Prism inside OpenUI's CodeBlock (vite.config.ts swaps it in).
// The real one carries every Prism grammar, so it is its own chunk, fetched when a reply first shows
// code; a reply without code never loads it. Until it arrives the code is shown as plain text.
const Prism = lazy(() => import("react-syntax-highlighter/dist/esm/prism"));

type Props = ComponentProps<typeof Prism>;

function Plain({ PreTag, className: cls, children }: Props) {
  const Tag = (PreTag ?? "pre") as "div";
  return (
    <Tag {...{ className: cls }}>
      <code style={{ whiteSpace: "pre" }}>{children}</code>
    </Tag>
  );
}

export function LazyPrism(props: Props) {
  return (
    <Suspense fallback={<Plain {...props} />}>
      <Prism {...props} />
    </Suspense>
  );
}
