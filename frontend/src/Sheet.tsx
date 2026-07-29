import { useCallback, useEffect, useRef, type ReactNode } from "react";

export interface SheetProps {
  title: string;
  /** Optional one-line context under the title (e.g. transport). */
  subtitle?: string;
  /** Extra class on the panel (e.g. sheet--settings). */
  panelClass?: string;
  /**
   * Fixed strip between the header and the scrolling body — a tab list, a
   * filter. Lives outside the scroll region on purpose: navigation that
   * scrolls away is not navigation.
   */
  toolbar?: ReactNode;
  onClose: () => void;
  children: ReactNode;
}

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * Modal surface: a bottom sheet on a phone, a centred dialog on a wide screen.
 *
 * Focus is trapped and restored, Escape closes, and the backdrop is inert to
 * scroll. Written once so every overlay in the app behaves identically.
 */
export function Sheet({ title, subtitle, panelClass, toolbar, onClose, children }: SheetProps) {
  const panel = useRef<HTMLDivElement>(null);
  const body = useRef<HTMLDivElement>(null);
  const restoreTo = useRef<HTMLElement | null>(null);

  const focusables = useCallback(
    () => Array.from(panel.current?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? []),
    [],
  );

  useEffect(() => {
    restoreTo.current = document.activeElement as HTMLElement | null;

    // Focus the scroll region (not the first button). In the menubar WKWebView,
    // trackpad scroll only reaches an overflow container after it is focused;
    // focusing Close left the body unable to scroll until a click.
    body.current?.focus({ preventScroll: true });

    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    return () => {
      document.body.style.overflow = prev;
      restoreTo.current?.focus?.();
    };
  }, []);

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
        return;
      }
      if (e.key !== "Tab") return;
      const items = focusables();
      if (items.length === 0) return;
      const first = items[0];
      const last = items[items.length - 1];
      const active = document.activeElement;
      // Body is tabIndex=-1; treat it as outside the cycle.
      if (e.shiftKey && (active === first || active === body.current)) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && active === last) {
        e.preventDefault();
        first.focus();
      }
    },
    [focusables, onClose],
  );

  return (
    <div className="scrim" onMouseDown={onClose}>
      <div
        className={["sheet", toolbar ? "sheet--tabbed" : null, panelClass]
          .filter(Boolean)
          .join(" ")}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        ref={panel}
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={onKeyDown}
      >
        <header className="sheet__head">
          <div className="sheet__titles">
            <h2>{title}</h2>
            {subtitle ? <p className="sheet__sub mono">{subtitle}</p> : null}
          </div>
          <button type="button" className="btn btn--icon" onClick={onClose} aria-label="Close">
            <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
              <path
                d="m4 4 8 8M12 4l-8 8"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.75"
                strokeLinecap="round"
              />
            </svg>
          </button>
        </header>
        {toolbar}
        <div
          className="sheet__body"
          ref={body}
          tabIndex={-1}
          // Keep wheel/trackpad on the body even if a child blurs.
          onWheel={(e) => e.stopPropagation()}
        >
          {children}
        </div>
      </div>
    </div>
  );
}
