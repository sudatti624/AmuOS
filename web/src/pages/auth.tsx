import { useState, type FormEvent } from "react";
import { Link, Navigate, useLocation, useNavigate } from "react-router";
import { useSession } from "@/app/session";
import { ErrorText, Field } from "@/components/common";
import { AuthShell } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, errorMessage, passwordBytes, usernamePattern, type User } from "@/lib/api";

const passwordHint = "12〜128バイト。長めのフレーズがおすすめです。";

function passwordProblem(password: string, confirmation: string) {
  const bytes = passwordBytes(password);
  if (bytes < 12 || bytes > 128) return "パスワードは12〜128バイトで入力してください。";
  if (password !== confirmation) return "確認用のパスワードが一致しません。";
  return null;
}

function Notice({ children }: { children: string }) {
  return (
    <p role="status" className="rounded-md border bg-muted/60 px-3 py-2 text-sm">
      {children}
    </p>
  );
}

export function LoginPage() {
  const { me, login, notice } = useSession();
  const location = useLocation();
  const navigate = useNavigate();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const message = (location.state as { notice?: string } | null)?.notice ?? notice;

  if (me) return <Navigate to="/" replace />;

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await login(username.trim().toLowerCase(), password);
      void navigate("/", { replace: true });
    } catch (failure) {
      setError(
        errorMessage(failure, { unauthorized: "ユーザー名またはパスワードが正しくありません。" }),
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthShell title="ログイン" description="AmuOSの管理画面にログインします。">
      {message && <Notice>{message}</Notice>}
      <form onSubmit={submit} className="grid gap-4">
        <Field id="username" label="ユーザー名">
          <Input
            id="username"
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
            autoFocus
          />
        </Field>
        <Field id="password" label="パスワード">
          <Input
            id="password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </Field>
        {error && <ErrorText>{error}</ErrorText>}
        <Button type="submit" disabled={busy}>
          ログイン
        </Button>
      </form>
      <div className="flex flex-col gap-1 text-sm text-muted-foreground">
        <Link to="/invite" className="text-primary underline-offset-4 hover:underline">
          招待を受け取った方はこちら
        </Link>
        <Link to="/setup" className="text-primary underline-offset-4 hover:underline">
          初回設定（最初のオーナーの作成）
        </Link>
      </div>
    </AuthShell>
  );
}

export function SetupPage() {
  const navigate = useNavigate();
  const [token, setToken] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const name = username.trim().toLowerCase();
    const problem = !usernamePattern.test(name)
      ? "ユーザー名は3〜64文字の英小文字・数字・「_」「-」「.」で入力してください。"
      : passwordProblem(password, confirmation);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.post(
        "/setup",
        { token: token.trim(), username: name, password },
        { credentialCheck: true },
      );
      void navigate("/login", {
        replace: true,
        state: { notice: "オーナーを作成しました。ログインしてください。" },
      });
    } catch (failure) {
      setError(
        errorMessage(failure, {
          unauthorized: "初回設定トークンが正しくないか、有効期限（10分）が切れています。",
          conflict: "すでに初期設定が完了しています。ログインしてください。",
        }),
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthShell title="初回設定" description="最初のオーナーアカウントを作成します。">
      <form onSubmit={submit} className="grid gap-4">
        <Field
          id="token"
          label="初回設定トークン"
          hint="サーバー上で --bootstrap-file を指定して発行したトークンです（有効期限10分・1回限り）。"
        >
          <Input
            id="token"
            autoComplete="off"
            spellCheck={false}
            value={token}
            onChange={(e) => setToken(e.target.value)}
            required
          />
        </Field>
        <Field id="username" label="ユーザー名" hint="3〜64文字の英小文字・数字・「_」「-」「.」">
          <Input
            id="username"
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
          />
        </Field>
        <Field id="password" label="パスワード" hint={passwordHint}>
          <Input
            id="password"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </Field>
        <Field id="confirmation" label="パスワード（確認）">
          <Input
            id="confirmation"
            type="password"
            autoComplete="new-password"
            value={confirmation}
            onChange={(e) => setConfirmation(e.target.value)}
            required
          />
        </Field>
        {error && <ErrorText>{error}</ErrorText>}
        <Button type="submit" disabled={busy}>
          オーナーを作成
        </Button>
      </form>
      <Link to="/login" className="text-sm text-primary underline-offset-4 hover:underline">
        ログイン画面へ戻る
      </Link>
    </AuthShell>
  );
}

export function InvitePage() {
  const navigate = useNavigate();
  const [token, setToken] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const problem = passwordProblem(password, confirmation);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const user = await api.post<User>(
        "/invitations/accept",
        { token: token.trim(), password },
        { credentialCheck: true },
      );
      void navigate("/login", {
        replace: true,
        state: {
          notice: `アカウント「${user.username}」を作成しました。このユーザー名でログインしてください。`,
        },
      });
    } catch (failure) {
      setError(
        errorMessage(failure, {
          unauthorized:
            "招待トークンが正しくないか、有効期限切れ・使用済み・取り消し済みです。招待した方に確認してください。",
        }),
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthShell
      title="招待を受け取る"
      description="受け取った招待トークンで、自分のアカウントを作成します。"
    >
      <form onSubmit={submit} className="grid gap-4">
        <Field
          id="token"
          label="招待トークン"
          hint="ユーザー名と権限は、招待した方が設定済みです。"
        >
          <Input
            id="token"
            autoComplete="off"
            spellCheck={false}
            value={token}
            onChange={(e) => setToken(e.target.value)}
            required
          />
        </Field>
        <Field id="password" label="パスワード" hint={passwordHint}>
          <Input
            id="password"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </Field>
        <Field id="confirmation" label="パスワード（確認）">
          <Input
            id="confirmation"
            type="password"
            autoComplete="new-password"
            value={confirmation}
            onChange={(e) => setConfirmation(e.target.value)}
            required
          />
        </Field>
        {error && <ErrorText>{error}</ErrorText>}
        <Button type="submit" disabled={busy}>
          アカウントを作成
        </Button>
      </form>
      <Link to="/login" className="text-sm text-primary underline-offset-4 hover:underline">
        ログイン画面へ戻る
      </Link>
    </AuthShell>
  );
}
