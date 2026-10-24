import "./zodConfig"; // must stay first: see the file
import { createLibrary, defineComponent } from "@openuidev/react-lang";
import { lazy, Suspense, useContext, useId, useState } from "react";
import { z } from "zod/v4";
import { MAX_REPLY, ReplyContext } from "./reply";

// Charts are drawn by a separate on-demand chunk (PERF-001): the library holds only the schema.
const ChartView = lazy(() => import("./Chart"));

// The component allowlist for model-generated UI (PRD §7.3, §7.4). Everything a
// model can put on screen is in this file, every value rendered as React text.
// There is deliberately no link, image, iframe, form submit or URL here. The only
// interactive components (Button, Input, Choice) do one thing: put text in the
// reader's composer through ReplyContext. They never send, navigate, fetch or
// mutate, so no backend route sits behind them (PRD §7.4).
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

const Chart = defineComponent({
  name: "Chart",
  description: "A bar, line or pie chart. One number per label.",
  props: z.object({
    kind: z.enum(["bar", "line", "pie"]),
    title: text(200),
    labels: z.array(text(80)).min(1).max(24),
    values: z.array(z.number().finite()).min(1).max(24),
  }),
  // The gate (validate.ts) has already checked that labels and values pair up.
  component: ({ props }) => (
    <Suspense fallback={<p className="label">Loading chart…</p>}>
      <ChartView {...props} />
    </Suspense>
  ),
});

// Without a composer to write into (no provider) the button is disabled, not pretending.
function ReplyButton({ label, text: reply, disabled }: { label: string; text: string; disabled?: boolean }) {
  const add = useContext(ReplyContext);
  return (
    <button type="button" className="ou-button" disabled={!add || disabled} onClick={() => add?.(reply)}>
      {label}
    </button>
  );
}

const Button = defineComponent({
  name: "Button",
  description: "A button that adds the given reply text to the reader's message box. It does not send anything.",
  props: z.object({ label: text(80), reply: text(MAX_REPLY) }),
  component: ({ props }) => <ReplyButton label={props.label} text={props.reply} />,
});

const Input = defineComponent({
  name: "Input",
  description: "A text field. Its text is added to the reader's message box as 'label: text'. It does not send anything.",
  props: z.object({ label: text(80), placeholder: text(80).optional() }),
  component: function InputView({ props }) {
    const [v, setV] = useState("");
    return (
      <label className="ou-field">
        <span>{props.label}</span>
        <input type="text" value={v} maxLength={300} placeholder={props.placeholder} onChange={(e) => setV(e.target.value)} />
        <ReplyButton label="Add to message" disabled={v.trim() === ""} text={`${props.label}: ${v.trim()}`} />
      </label>
    );
  },
});

const Choice = defineComponent({
  name: "Choice",
  description: "A pick-one list. The chosen option is added to the reader's message box as 'label: option'. It does not send anything.",
  props: z.object({ label: text(80), options: z.array(text(80)).min(2).max(12) }),
  component: function ChoiceView({ props }) {
    const [i, setI] = useState(-1);
    const group = useId(); // per instance: two Choices with the same label must not share a radio group
    return (
      <fieldset className="ou-field">
        <legend>{props.label}</legend>
        {props.options.map((o, k) => (
          <label key={k} className="ou-option">
            <input type="radio" name={group} checked={i === k} onChange={() => setI(k)} /> {o}
          </label>
        ))}
        <ReplyButton label="Add to message" disabled={i < 0} text={`${props.label}: ${props.options[i] ?? ""}`} />
      </fieldset>
    );
  },
});

// Containers take other allowlisted components only (the schema refs enforce it).
const children = () => z.array(z.union([Text.ref, Heading.ref, Badge.ref, Metric.ref, BulletList.ref, Table.ref, Divider.ref, Chart.ref, Button.ref, Input.ref, Choice.ref])).max(40);

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
  props: z.object({ children: z.array(z.union([Text.ref, Heading.ref, Badge.ref, Metric.ref, BulletList.ref, Table.ref, Divider.ref, Chart.ref, Button.ref, Input.ref, Choice.ref, Card.ref])).max(40) }),
  component: ({ props, renderNode }) => <div className="ou-stack">{renderNode(props.children)}</div>,
});

export const UI_VERSION = "1";

export const library = createLibrary({
  components: [Stack, Card, Text, Heading, Badge, Metric, BulletList, Table, Divider, Chart, Button, Input, Choice],
  root: "Stack",
});

export const ALLOWED_COMPONENTS: readonly string[] = ["Stack", "Card", "Text", "Heading", "Badge", "Metric", "BulletList", "Table", "Divider", "Chart", "Button", "Input", "Choice"];
