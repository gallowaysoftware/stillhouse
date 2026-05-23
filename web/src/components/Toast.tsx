import { ReactNode, createContext, useCallback, useContext, useEffect, useRef, useState } from "react";

export type ToastTone = "info" | "success" | "warning" | "danger";

type Toast = {
  id: number;
  tone: ToastTone;
  message: string;
};

type ToastApi = (tone: ToastTone, message: string) => void;

const ToastContext = createContext<ToastApi | null>(null);

/**
 * useToast — fire-and-forget transient banners for mutation outcomes.
 * Imperative on purpose: callers like `onSuccess: () => toast("success", "Saved")`
 * don't want to manage local toast state.
 *
 * Toasts auto-dismiss after 4s. Multiple stack bottom-up; click X to dismiss.
 */
export function useToast() {
  const ctx = useContext(ToastContext);
  if (!ctx) {
    // Loud in dev, silent in prod — same pattern as useConfirm.
    if (typeof window !== "undefined") console.error("useToast called outside <ToastProvider/>");
    return () => {};
  }
  return ctx;
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const counter = useRef(0);
  const timers = useRef<Map<number, ReturnType<typeof setTimeout>>>(new Map());

  const dismiss = useCallback((id: number) => {
    setToasts((ts) => ts.filter((t) => t.id !== id));
    const timer = timers.current.get(id);
    if (timer) {
      clearTimeout(timer);
      timers.current.delete(id);
    }
  }, []);

  const push: ToastApi = useCallback((tone, message) => {
    const id = ++counter.current;
    setToasts((ts) => [...ts, { id, tone, message }]);
    const timer = setTimeout(() => dismiss(id), 4000);
    timers.current.set(id, timer);
  }, [dismiss]);

  useEffect(() => {
    return () => {
      timers.current.forEach((t) => clearTimeout(t));
      timers.current.clear();
    };
  }, []);

  return (
    <ToastContext.Provider value={push}>
      {children}
      <div className="pointer-events-none fixed inset-x-0 bottom-4 z-50 flex flex-col items-center gap-2 px-4">
        {toasts.map((t) => (
          <div
            key={t.id}
            className={`pointer-events-auto flex max-w-md items-start gap-3 rounded-md border border-l-4 px-3 py-2 text-sm shadow-card-dark ${toneClass(t.tone)}`}
            role="status"
          >
            <span className="flex-1">{t.message}</span>
            <button
              onClick={() => dismiss(t.id)}
              className="text-fg-muted hover:text-fg"
              aria-label="Dismiss"
            >
              ×
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

function toneClass(tone: ToastTone): string {
  switch (tone) {
    case "success":
      return "border-success/40 border-l-success bg-surface-2 text-success-fg";
    case "warning":
      return "border-warning/40 border-l-warning bg-surface-2 text-warning-fg";
    case "danger":
      return "border-danger/40 border-l-danger bg-surface-2 text-danger-fg";
    default:
      return "border-info/40 border-l-info bg-surface-2 text-info-fg";
  }
}
