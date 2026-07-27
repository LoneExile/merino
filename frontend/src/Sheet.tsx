import { useCallback, useEffect, useRef, type ReactNode } from "react";

export interface SheetProps {
  title: string;
  onClose: () => void;
  children: ReactNode;
}

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * Modal surface: a bottom sheet on a phone, a centred dialog on a wide screen.
 *
 * Focus is trapped and restored, Escape closes, and the backdrop is inert to
 * scroll. Written once so every overlay in the app behaves identically —
 * a settings panel that traps focus differently from a rename dialog is a bug
 * a keyboard user finds immediately.
 */
export function Sheet({ title, onClose, children }: SheetProps) {
  const panel = useRef<HTMLDivElement>(null);
  const restoreTo = useRef<HTMLElement | null>(null);

  const focusables = useCallback(
    () => Array.from(panel.current?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? []),
    [],
  );

  useEffect(() => {
    restoreTo.current = document.activeElement as HTMLElement | null;
    focusables()[0]?.focus();

    // Lock the page behind the sheet. Without this, scrolling the sheet on iOS
    // scrolls the list underneath once the sheet hits its end.
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    return () => {
      document.body.style.overflow = prev;
      restoreTo.current?.focus?.();
    };
  }, [focusables]);

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
      if (e.shiftKey && active === first) {
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
        className="sheet"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        ref={panel}
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={onKeyDown}
      >
        <header className="sheet__head">
          <h2>{title}</h2>
          <button className="btn btn--icon" onClick={onClose} aria-label="Close">
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
        <div className="sheet__body">{children}</div>
      </div>
    </div>
  );
}
