import { useEffect, useMemo, useRef, useState } from "react";

export interface Command {
  id: string;
  label: string;
  hint?: string;
  run: () => void;
}

export interface PaletteProps {
  commands: Command[];
  onClose: () => void;
}

/**
 * ⌘K command palette.
 *
 * Cobalt's signature interactive move, and here it earns its place rather than
 * decorating: with a dozen agents across several workspaces, typing three
 * letters of a project name beats scrolling a list — especially one-handed on a
 * phone, where the keyboard is already open.
 */
export function Palette({ commands, onClose }: PaletteProps) {
  const [q, setQ] = useState("");
  const [i, setI] = useState(0);
  const listRef = useRef<HTMLUListElement>(null);

  const hits = useMemo(() => {
    const needle = q.trim().toLowerCase();
    if (!needle) return commands;
    return commands.filter((c) =>
      `${c.label} ${c.hint ?? ""}`.toLowerCase().includes(needle),
    );
  }, [commands, q]);

  // Clamp rather than reset: filtering down to fewer results should not silently
  // move the selection to something the user was not looking at.
  useEffect(() => {
    setI((prev) => Math.min(prev, Math.max(hits.length - 1, 0)));
  }, [hits.length]);

  useEffect(() => {
    listRef.current
      ?.querySelector<HTMLElement>(`[data-i="${i}"]`)
      ?.scrollIntoView({ block: "nearest" });
  }, [i]);

  return (
    <div className="scrim scrim--top" onMouseDown={onClose}>
      <div
        className="palette"
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            onClose();
          } else if (e.key === "ArrowDown") {
            e.preventDefault();
            setI((v) => Math.min(v + 1, hits.length - 1));
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            setI((v) => Math.max(v - 1, 0));
          } else if (e.key === "Enter") {
            e.preventDefault();
            const hit = hits[i];
            if (hit) {
              onClose();
              hit.run();
            }
          }
        }}
      >
        <input
          className="palette__input mono"
          autoFocus
          value={q}
          placeholder="Jump to an agent, or a command…"
          aria-label="Search commands"
          aria-controls="palette-list"
          onChange={(e) => setQ(e.target.value)}
        />
        <ul className="palette__list" id="palette-list" role="listbox" ref={listRef}>
          {hits.length === 0 && <li className="palette__empty">No match</li>}
          {hits.map((c, n) => (
            <li key={c.id}>
              <button
                data-i={n}
                role="option"
                aria-selected={n === i}
                className={`palette__row${n === i ? " is-on" : ""}`}
                onMouseEnter={() => setI(n)}
                onClick={() => {
                  onClose();
                  c.run();
                }}
              >
                <span>{c.label}</span>
                {c.hint && <span className="mono palette__hint">{c.hint}</span>}
              </button>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
