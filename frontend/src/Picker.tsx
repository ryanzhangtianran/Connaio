import { useState } from "react";
import { useClickAway } from "./hooks";

// Picker is a compact dropdown for a card's head. Not a <select>: on Linux its popup is GTK's own
// menu, out of keeping with the page; the menu is fixed to the window so the card doesn't clip it.
export function Picker({ value, options, onChange, label, className = "" }: { value: string; options: string[]; onChange: (v: string) => void; label?: string; className?: string }) {
  const [at, setAt] = useState<DOMRect | null>(null);
  useClickAway(!!at, () => setAt(null));
  return (
    <>
      {/* the click stops here: opening attaches the click-away listener, which it would reach next */}
      <button className={`picker ${className}`} onClick={(e) => { e.stopPropagation(); setAt(at ? null : e.currentTarget.getBoundingClientRect()); }}>
        <span>{label ?? (value || "—")}</span>
        <svg viewBox="0 0 10 6" width="10" height="6"><path d="M1 1l4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.5" /></svg>
      </button>
      {at && (
        // it opens toward the side with room: from a picker in the left half (the floating monitor's) to the right
        <div className="menu picker-menu" style={{
          top: at.bottom + 4, minWidth: at.width, maxHeight: Math.min(280, window.innerHeight - at.bottom - 12),
          ...(at.left < window.innerWidth / 2 ? { left: at.left } : { right: window.innerWidth - at.right }),
        }}>
          {options.map((o) => <button key={o} className={o === value ? "current" : ""} onClick={() => onChange(o)}>{o}</button>)}
        </div>
      )}
    </>
  );
}
