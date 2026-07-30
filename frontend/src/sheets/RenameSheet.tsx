import { useState } from "react";
import type { Agent } from "../../bindings/github.com/LoneExile/merino/internal/app";
import { agentTitle } from "../agentName";
import type { Client, RenameKind } from "../client";
import { Sheet } from "../Sheet";

export interface RenameTarget {
  kind: RenameKind;
  id: string;
  current: string;
  /**
   * The operator-set name, when this target already has one. Only panes
   * carry it: herdr labels tabs and workspaces too, but Merino does not
   * project those, and inventing one from the id would be a lie.
   */
  named?: string;
}

export function renameTargets(agent: Agent): RenameTarget[] {
  return [
    {
      kind: "pane",
      id: agent.paneId,
      current: agentTitle(agent),
      named: agent.label?.trim() || undefined,
    },
    { kind: "tab", id: agent.tabId, current: agent.tabId },
    { kind: "workspace", id: agent.workspaceId, current: agent.workspaceId },
  ];
}

export interface RenameSheetProps {
  client: Client;
  agent: Agent;
  onClose: () => void;
}

export function RenameSheet({ client, agent, onClose }: RenameSheetProps) {
  const targets = renameTargets(agent);
  const [kind, setKind] = useState<RenameKind>("pane");
  const [name, setName] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const target = targets.find((t) => t.kind === kind)!;

  const submit = async () => {
    const next = name.trim();
    if (!next || busy) return;
    setBusy(true);
    setErr(null);
    try {
      await client.rename?.(kind, target.id, next);
      onClose();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  return (
    <Sheet title="Rename" onClose={onClose}>
      <div className="seg" role="radiogroup" aria-label="What to rename">
        {targets.map((t) => (
          <button
            key={t.kind}
            role="radio"
            aria-checked={kind === t.kind}
            className={`seg__opt${kind === t.kind ? " is-on" : ""}`}
            onClick={() => setKind(t.kind)}
          >
            {t.kind}
          </button>
        ))}
      </div>

      {/* Naming something you cannot see the current name of is guesswork,
        * and it is why a successful rename read as a no-op. */}
      <p className="hint mono">
        {target.id}
        {target.named && <> · {target.named}</>}
      </p>

      <label className="field">
        <span className="field__label">New name</span>
        <input
          className="input"
          value={name}
          maxLength={120}
          autoFocus
          placeholder={target.current}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              void submit();
            }
          }}
        />
      </label>

      {err && (
        <p className="composer__err" role="alert">
          {err}
        </p>
      )}

      <div className="sheet__actions">
        <button className="btn" onClick={onClose}>
          Cancel
        </button>
        <button className="btn btn--primary" disabled={busy || !name.trim()} onClick={() => void submit()}>
          Rename {kind}
        </button>
      </div>
    </Sheet>
  );
}
