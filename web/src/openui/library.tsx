import "./zodConfig"; // must stay first: see the file
import { createLibrary, defineComponent } from "@openuidev/react-lang";
import { z } from "zod/v4";

// The component allowlist for model-generated UI (PRD §7.3, §7.4). Everything a
// model can put on screen is in this file: display components only, every value
// rendered as React text. There is deliberately no button, form, link, image,
// iframe or action here, so a block cannot navigate, fetch, submit or mutate
// anything; adding one needs the secured Go API behind it first (slice 6+).
// A name that is not defined here is "unknown-component" to the parser.

export const MAX_TEXT = 4_000;
const text = (max = MAX_TEXT) => z.string().max(max);

const Text = defineComponent({
  name: "Text",
  description: "A paragraph of plain text.",
  props: z.object({ value: text() }),
  component: ({ props }) => <p className="ou-text">{props.value}</p>,
});

const Heading = defineComponent({
  name: "Heading",
  description: "A short section heading.",
  props: z.object({ value: text(200) }),
  component: ({ props }) => <h4 className="ou-heading">{props.value}</h4>,
});

const Badge = defineComponent({
  name: "Badge",
  description: "A short label such as a status.",
  props: z.object({ value: text(60) }),
  component: ({ props }) => <span className="ou-badge">{props.value}</span>,
});

const Metric = defineComponent({
  name: "Metric",
  description: "A labelled number or short value.",
  props: z.object({ label: text(80), value: text(80) }),
  component: ({ props }) => (
    <div className="ou-metric">
      <span className="ou-metric-value">{props.value}</span>
      <span className="ou-metric-label">{props.label}</span>
    </div>
  ),
});

const BulletList = defineComponent({
  name: "BulletList",
  description: "A bulleted list of short plain-text items.",
  props: z.object({ items: z.array(text(500)).max(50) }),
  component: ({ props }) => (
    <ul className="ou-list">
      {props.items.map((it, i) => (
        <li key={i}>{it}</li>
      ))}
    </ul>
  ),
});

const Table = defineComponent({
  name: "Table",
  description: "A table of plain-text cells. Every row has one cell per column.",
  props: z.object({ columns: z.array(text(80)).min(1).max(12), rows: z.array(z.array(text(500)).max(12)).max(100) }),
  component: ({ props }) => (
    <div className="ou-table-wrap" tabIndex={0} role="region" aria-label="Table">
      <table className="ou-table">
        <thead>
          <tr>
            {props.columns.map((c, i) => (
              <th key={i} scope="col">
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {props.rows.map((r, i) => (
            <tr key={i}>
              {props.columns.map((_, j) => (
                <td key={j}>{r[j] ?? ""}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  ),
});

const Divider = defineComponent({
  name: "Divider",
  description: "A thin horizontal rule between sections.",
  props: z.object({}),
  component: () => <hr className="ou-divider" />,
});

// Containers take other allowlisted components only (the schema refs enforce it).
const children = () => z.array(z.union([Text.ref, Heading.ref, Badge.ref, Metric.ref, BulletList.ref, Table.ref, Divider.ref])).max(40);

const Card = defineComponent({
  name: "Card",
  description: "A titled box that groups other components.",
  props: z.object({ title: text(200), children: children() }),
  component: ({ props, renderNode }) => (
    <section className="ou-card" aria-label={props.title}>
      <h4 className="ou-heading">{props.title}</h4>
      {renderNode(props.children)}
    </section>
  ),
});

const Stack = defineComponent({
  name: "Stack",
  description: "Lays components out top to bottom. The usual root.",
  props: z.object({ children: z.array(z.union([Text.ref, Heading.ref, Badge.ref, Metric.ref, BulletList.ref, Table.ref, Divider.ref, Card.ref])).max(40) }),
  component: ({ props, renderNode }) => <div className="ou-stack">{renderNode(props.children)}</div>,
});

export const UI_VERSION = "1";

export const library = createLibrary({
  components: [Stack, Card, Text, Heading, Badge, Metric, BulletList, Table, Divider],
  root: "Stack",
});

export const ALLOWED_COMPONENTS: readonly string[] = ["Stack", "Card", "Text", "Heading", "Badge", "Metric", "BulletList", "Table", "Divider"];
