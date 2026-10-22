/** What is shown when a block cannot be rendered: its source, as inert text. No OpenUI import, so it is in the main bundle. */
export function SourceFallback({ source, why }: { source: string; why: string }) {
  return (
    <div className="ou-fallback">
      <p className="label">This rich block could not be displayed ({why}). Showing its source.</p>
      <pre className="plain">
        <code>{source}</code>
      </pre>
    </div>
  );
}
