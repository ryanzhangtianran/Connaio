import { useEffect, useId } from "react";

// Dialog is every dialog's sheet: its title (and what it is for) on top, the body scrolling, the
// buttons at its foot; bar, if any, stays between the title and the body. A click outside or Esc
// closes it unless locked: edits not yet saved, or work still running, which only its buttons end.
export default function Dialog({ title, sub, bar, className = "", bodyClass = "", locked = false, onClose, foot, children }: {
  title: React.ReactNode; sub?: React.ReactNode; bar?: React.ReactNode; className?: string; bodyClass?: string; locked?: boolean;
  onClose: () => void; foot: React.ReactNode; children: React.ReactNode;
}) {
  useEffect(() => {
    if (locked) return;
    const key = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", key);
    return () => document.removeEventListener("keydown", key);
  }, [locked, onClose]);
  const titleId = useId();
  return (
    <div className="modal-bg" onClick={locked ? undefined : onClose}>
      <div className={`sheet ${className}`} role="dialog" aria-modal="true" aria-labelledby={titleId} onClick={(e) => e.stopPropagation()}>
        <div className="sheet-head"><div className="sheet-title" id={titleId}>{title}</div>{sub && <div className="muted small sheet-sub">{sub}</div>}</div>
        {bar}
        <div className={`sheet-body ${bodyClass}`}>{children}</div>
        <div className="sheet-foot">{foot}</div>
      </div>
    </div>
  );
}
