import { useEffect, useId, useRef } from "react";
import { store } from "../store";

export const close = () => store.setUI({ dialog: undefined });

export function Modal({ title, children, onClose }: { title: string; children: React.ReactNode; onClose: () => void }) {
  const id = useId();
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    ref.current?.querySelector<HTMLElement>("[autofocus], input, textarea, select, button")?.focus();
    return () => prev?.focus?.();
  }, []);
  return (
    <div className="overlay" onMouseDown={onClose}>
      <div
        className="dialog"
        role="dialog"
        aria-modal
        aria-labelledby={id}
        ref={ref}
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.key === "Escape" && onClose()}
      >
        <h2 id={id}>{title}</h2>
        {children}
      </div>
    </div>
  );
}

