import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { CircleAlertIcon, CircleCheckIcon, XIcon } from "lucide-react";
import { cn } from "@/lib/utils";

type Kind = "success" | "error";
interface Notification {
  id: number;
  kind: Kind;
  message: string;
}

const NotifyContext = createContext<((kind: Kind, message: string) => void) | null>(null);

export function NotificationProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<Notification[]>([]);
  const next = useRef(0);
  const dismiss = useCallback(
    (id: number) => setItems((current) => current.filter((n) => n.id !== id)),
    [],
  );
  const notify = useCallback(
    (kind: Kind, message: string) => {
      const id = next.current++;
      setItems((current) => [...current.slice(-3), { id, kind, message }]);
      // Errors stay until dismissed so they are not missed.
      if (kind === "success") window.setTimeout(() => dismiss(id), 5000);
    },
    [dismiss],
  );
  return (
    <NotifyContext.Provider value={notify}>
      {children}
      <div
        aria-live="polite"
        className="pointer-events-none fixed right-4 bottom-4 z-[60] flex w-[min(24rem,calc(100%-2rem))] flex-col gap-2"
      >
        {items.map((item) => (
          <div
            key={item.id}
            role={item.kind === "error" ? "alert" : "status"}
            className={cn(
              "pointer-events-auto flex items-start gap-3 rounded-lg border bg-card p-3 text-sm shadow-lg",
              item.kind === "error" && "border-destructive/50",
            )}
          >
            {item.kind === "error" ? (
              <CircleAlertIcon className="mt-0.5 size-4 shrink-0 text-destructive" />
            ) : (
              <CircleCheckIcon className="mt-0.5 size-4 shrink-0 text-success" />
            )}
            <p className="flex-1">{item.message}</p>
            <button
              type="button"
              onClick={() => dismiss(item.id)}
              className="rounded-sm opacity-70 hover:opacity-100 focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
            >
              <XIcon className="size-4" />
              <span className="sr-only">通知を閉じる</span>
            </button>
          </div>
        ))}
      </div>
    </NotifyContext.Provider>
  );
}

export function useNotify() {
  const notify = useContext(NotifyContext);
  if (!notify) throw new Error("useNotify outside NotificationProvider");
  return useMemo(
    () => ({
      success: (message: string) => notify("success", message),
      error: (message: string) => notify("error", message),
    }),
    [notify],
  );
}
