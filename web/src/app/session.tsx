import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { api, ApiError, getCsrf, onUnauthorized, setCsrf, type Me } from "@/lib/api";

interface SessionValue {
  me: Me | null;
  loading: boolean;
  // Shown on the login page after the session ended.
  notice: string | null;
  login: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  refresh: () => Promise<void>;
  end: (notice: string) => void;
}

const SessionContext = createContext<SessionValue | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [me, setMe] = useState<Me | null>(null);
  const [loading, setLoading] = useState(true);
  const [notice, setNotice] = useState<string | null>(null);

  const end = useCallback((message: string) => {
    setCsrf(null);
    setMe(null);
    setNotice(message);
  }, []);

  const refresh = useCallback(async () => {
    try {
      setMe(await api.get<Me>("/me"));
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) setMe(null);
      else throw error;
    }
  }, []);

  useEffect(() => {
    onUnauthorized(() =>
      end("セッションの有効期限が切れたか、権限が変更されました。もう一度ログインしてください。"),
    );
    return () => onUnauthorized(null);
  }, [end]);

  useEffect(() => {
    let cancelled = false;
    // Without this tab's CSRF token the session can read but not change state,
    // so a new login is required.
    if (!getCsrf()) {
      setLoading(false);
      return;
    }
    api
      .get<Me>("/me")
      .then((value) => !cancelled && setMe(value))
      .catch(() => !cancelled && setMe(null))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, []);

  const login = useCallback(async (username: string, password: string) => {
    const result = await api.post<{ user_id: string; csrf_token: string }>(
      "/auth/login",
      { username, password },
      { credentialCheck: true },
    );
    setCsrf(result.csrf_token);
    setNotice(null);
    setMe(await api.get<Me>("/me"));
  }, []);

  const logout = useCallback(async () => {
    try {
      await api.post("/auth/logout", undefined);
    } catch {
      // The local session is cleared even if the server session already ended.
    }
    end("ログアウトしました。");
  }, [end]);

  const value = useMemo(
    () => ({ me, loading, notice, login, logout, refresh, end }),
    [me, loading, notice, login, logout, refresh, end],
  );
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession() {
  const value = useContext(SessionContext);
  if (!value) throw new Error("useSession outside SessionProvider");
  return value;
}

export function useMe() {
  const { me } = useSession();
  if (!me) throw new Error("useMe outside an authenticated route");
  return me;
}
