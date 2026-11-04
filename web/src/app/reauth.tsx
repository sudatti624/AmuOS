import {
  createContext,
  useCallback,
  useContext,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import { useSession } from "@/app/session";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api, ApiError, errorMessage } from "@/lib/api";
import { isOwner } from "@/lib/permissions";

// Thrown when the user closes the password prompt; callers ignore it.
export class ReauthCancelled extends Error {}

type Run = <T>(action: () => Promise<T>) => Promise<T>;
const ReauthContext = createContext<Run | null>(null);

export function ReauthProvider({ children }: { children: ReactNode }) {
  const { me } = useSession();
  const [open, setOpen] = useState(false);
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const pending = useRef<{ resolve: () => void; reject: (error: Error) => void } | null>(null);

  const prompt = useCallback(
    () =>
      new Promise<void>((resolve, reject) => {
        pending.current = { resolve, reject };
        setPassword("");
        setError(null);
        setOpen(true);
      }),
    [],
  );

  // Owner operations need a re-authentication from the last five minutes. The
  // server answers 403 without one, so ask for the password and retry once.
  const run = useCallback<Run>(
    async (action) => {
      try {
        return await action();
      } catch (error) {
        if (!(error instanceof ApiError) || error.status !== 403 || !me || !isOwner(me.grants))
          throw error;
        await prompt();
        return await action();
      }
    },
    [me, prompt],
  );

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.post("/auth/reauthenticate", { password }, { credentialCheck: true });
      setOpen(false);
      pending.current?.resolve();
      pending.current = null;
    } catch (failure) {
      setError(errorMessage(failure, { unauthorized: "パスワードが正しくありません。" }));
    } finally {
      setBusy(false);
    }
  };

  const cancel = () => {
    setOpen(false);
    pending.current?.reject(new ReauthCancelled());
    pending.current = null;
  };

  return (
    <ReauthContext.Provider value={run}>
      {children}
      <Dialog open={open} onOpenChange={(next) => !next && cancel()}>
        <DialogContent>
          <form onSubmit={submit} className="grid gap-4">
            <DialogHeader>
              <DialogTitle>パスワードの再確認</DialogTitle>
              <DialogDescription>
                オーナーの重要な操作のため、現在のパスワードをもう一度入力してください。確認は5分間有効です。
              </DialogDescription>
            </DialogHeader>
            <div className="grid gap-2">
              <Label htmlFor="reauth-password">パスワード</Label>
              <Input
                id="reauth-password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                required
                autoFocus
                aria-invalid={error !== null}
              />
              {error && <p className="text-sm text-destructive">{error}</p>}
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={cancel}>
                キャンセル
              </Button>
              <Button type="submit" disabled={busy || password === ""}>
                確認して続ける
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </ReauthContext.Provider>
  );
}

export function useOwnerAction() {
  const run = useContext(ReauthContext);
  if (!run) throw new Error("useOwnerAction outside ReauthProvider");
  return run;
}
