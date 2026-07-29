// Extracted from the 1052-line SettingsSheet, where every tab's state lived in
// one scope and all five JSX trees were built on every render. Each tab now
// owns exactly the state it uses, so a reader can follow one without holding
// the other four.

import type { ThemePref } from "../../theme";

const THEMES: { id: ThemePref; label: string }[] = [
  { id: "light", label: "Light" },
  { id: "dark", label: "Dark" },
  { id: "system", label: "System" },
];

const WRAP_OPTS: { value: boolean; label: string }[] = [
  { value: false, label: "Off" },
  { value: true, label: "On" },
];

export interface DisplayTabProps {
  pref: ThemePref;
  actual: "light" | "dark";
  onPref: (p: ThemePref) => void;
  wrap: boolean;
  onWrap: (w: boolean) => void;
  termFont: {
    px: number;
    zoomIn: () => void;
    zoomOut: () => void;
    canZoomIn: boolean;
    canZoomOut: boolean;
  };
}

export function DisplayTab({ pref, actual, onPref, wrap, onWrap, termFont }: DisplayTabProps) {
  return (
    <section className="settings-block" aria-labelledby="set-appear">
      <header className="settings-block__head">
        {/* Hidden, not absent: the DISPLAY tab already titles this on screen,
         * but the section still needs an accessible name. This is the only
         * tab whose sole heading restates its own tab label — every other
         * block keeps a visible one, which is what the deleted
         * .settings-pane--single rule used to get wrong. */}
        <h3 id="set-appear" className="sr-only">
          Appearance
        </h3>
      </header>
      <div className="settings-row">
        <div className="settings-row__meta">
          <span className="settings-row__label">Theme</span>
          <span className="settings-row__hint">
            {pref === "system" ? `Follows device · ${actual}` : `Locked · ${pref}`}
          </span>
        </div>
        <div className="seg seg--compact" role="radiogroup" aria-label="Theme">
          {THEMES.map((t) => (
            <button
              key={t.id}
              type="button"
              role="radio"
              aria-checked={pref === t.id}
              className={`seg__opt${pref === t.id ? " is-on" : ""}`}
              onClick={() => onPref(t.id)}
            >
              {t.label}
            </button>
          ))}
        </div>
      </div>
      <div className="settings-row">
        <div className="settings-row__meta">
          <span className="settings-row__label">Line wrap</span>
          <span className="settings-row__hint">
            {wrap ? "Long lines fold to the pane width" : "Scroll sideways for long lines"}
          </span>
        </div>
        <div className="seg seg--compact" role="radiogroup" aria-label="Wrap long lines">
          {WRAP_OPTS.map((o) => (
            <button
              key={String(o.value)}
              type="button"
              role="radio"
              aria-checked={wrap === o.value}
              className={`seg__opt${wrap === o.value ? " is-on" : ""}`}
              onClick={() => onWrap(o.value)}
            >
              {o.label}
            </button>
          ))}
        </div>
      </div>
      <div className="settings-row">
        <div className="settings-row__meta">
          <span className="settings-row__label">Terminal size</span>
          <span className="settings-row__hint">{termFont.px}px monospaced</span>
        </div>
        <div className="seg seg--compact" role="group" aria-label="Terminal font size">
          <button
            type="button"
            className="seg__opt"
            disabled={!termFont.canZoomOut}
            onClick={() => termFont.zoomOut()}
            aria-label="Decrease font size"
          >
            A−
          </button>
          <button
            type="button"
            className="seg__opt"
            disabled={!termFont.canZoomIn}
            onClick={() => termFont.zoomIn()}
            aria-label="Increase font size"
          >
            A+
          </button>
        </div>
      </div>
    </section>
  );
}
