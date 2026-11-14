import { useCallback, useState, type FormEvent } from "react";
import { useNotify } from "@/app/notifications";
import { ReauthCancelled, useOwnerAction } from "@/app/reauth";
import { useMe, useSession } from "@/app/session";
import { useLoad } from "@/app/use-load";
import { ErrorText, Field, Loading, PageHeader } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api, errorMessage, passwordBytes, type Retention, type RetentionRun } from "@/lib/api";
import { formatTime } from "@/lib/format";
import { canReadAuditSettings, isOwner } from "@/lib/permissions";
import { saveTheme, storedTheme, type Theme } from "@/lib/theme";
import { MonitoringSettingsCard } from "./monitoring-settings";

function ThemeCard() {
  const [theme, setTheme] = useState<Theme>(storedTheme);
  const options: { value: Theme; label: string }[] = [
    { value: "dark", label: "ダーク" },
    { value: "light", label: "ライト" },
    { value: "system", label: "OSの設定に合わせる" },
  ];
  return (
    <Card>
      <CardHeader>
        <CardTitle>表示</CardTitle>
        <CardDescription>このブラウザでの表示テーマです。</CardDescription>
      </CardHeader>
      <fieldset className="flex flex-wrap gap-4">
        <legend className="sr-only">テーマ</legend>
        {options.map((option) => (
          <label key={option.value} className="flex items-center gap-2 text-sm">
            <input
              type="radio"
              name="theme"
              value={option.value}
              checked={theme === option.value}
              onChange={() => {
                setTheme(option.value);
                saveTheme(option.value);
              }}
            />
            {option.label}
          </label>
        ))}
      </fieldset>
    </Card>
  );
}

function PasswordCard() {
  const { end } = useSession();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const bytes = passwordBytes(next);
    if (bytes < 12 || bytes > 128) {
      setError("新しいパスワードは12〜128バイトで入力してください。");
      return;
    }
    if (next !== confirmation) {
      setError("確認用のパスワードが一致しません。");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.post(
        "/auth/password",
        { current_password: current, new_password: next },
        { credentialCheck: true },
      );
      end(
        "パスワードを変更しました。すべてのセッションが終了したので、新しいパスワードでログインしてください。",
      );
    } catch (failure) {
      setError(errorMessage(failure, { unauthorized: "現在のパスワードが正しくありません。" }));
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>パスワードの変更</CardTitle>
        <CardDescription>
          変更すると、ほかの端末を含むすべてのセッションが終了します。
        </CardDescription>
      </CardHeader>
      <form onSubmit={submit} className="grid max-w-sm gap-4">
        <Field id="current-password" label="現在のパスワード">
          <Input
            id="current-password"
            type="password"
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            required
          />
        </Field>
        <Field id="new-password" label="新しいパスワード" hint="12〜128バイト">
          <Input
            id="new-password"
            type="password"
            autoComplete="new-password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
            required
          />
        </Field>
        <Field id="new-password-confirmation" label="新しいパスワード（確認）">
          <Input
            id="new-password-confirmation"
            type="password"
            autoComplete="new-password"
            value={confirmation}
            onChange={(e) => setConfirmation(e.target.value)}
            required
          />
        </Field>
        {error && <ErrorText>{error}</ErrorText>}
        <Button type="submit" className="w-fit" disabled={busy}>
          変更する
        </Button>
      </form>
    </Card>
  );
}

function RetentionCard() {
  const me = useMe();
  const notify = useNotify();
  const owner = useOwnerAction();
  const load = useCallback(() => api.get<Retention>("/audit/retention"), []);
  const { data, error, loading, reload } = useLoad(load);
  const [days, setDays] = useState<string | null>(null);
  const [automatic, setAutomatic] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const editable = isOwner(me.grants);

  const run = async (action: () => Promise<unknown>, success: (result: unknown) => string) => {
    setBusy(true);
    try {
      const result = await owner(action);
      notify.success(success(result));
      setDays(null);
      setAutomatic(null);
      await reload();
    } catch (failure) {
      if (!(failure instanceof ReauthCancelled)) notify.error(errorMessage(failure));
    } finally {
      setBusy(false);
    }
  };

  if (loading && !data) return <Loading />;
  if (!data) return error ? <ErrorText>{error}</ErrorText> : null;
  const daysValue = days ?? String(data.retention_days);
  const automaticValue = automatic ?? data.automatic_enabled;
  const daysNumber = Number(daysValue);
  const validDays = Number.isInteger(daysNumber) && daysNumber >= 1 && daysNumber <= 3650;

  return (
    <Card>
      <CardHeader>
        <CardTitle>監査ログの保持</CardTitle>
        <CardDescription>
          保持期間を過ぎた監査ログは削除されます。変更はオーナーだけができます。
        </CardDescription>
      </CardHeader>
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-sm">
        <dt className="text-muted-foreground">次回の自動実行</dt>
        <dd>{data.automatic_enabled ? formatTime(data.next_run_at) : "停止中"}</dd>
        <dt className="text-muted-foreground">前回の実行</dt>
        <dd>{data.last_run_at ? formatTime(data.last_run_at) : "まだありません"}</dd>
        {data.failure_count > 0 && (
          <>
            <dt className="text-muted-foreground">連続失敗</dt>
            <dd className="text-destructive">
              {data.failure_count}回{data.last_error_code ? `（${data.last_error_code}）` : ""}
            </dd>
          </>
        )}
      </dl>
      <form
        className="grid max-w-sm gap-4"
        onSubmit={(event) => {
          event.preventDefault();
          void run(
            () =>
              api.put("/audit/retention", {
                retention_days: daysNumber,
                automatic_enabled: automaticValue,
              }),
            () => "監査ログの保持設定を更新しました。",
          );
        }}
      >
        <Field id="retention-days" label="保持期間（日）" hint="1〜3650日。既定は180日です。">
          <Input
            id="retention-days"
            type="number"
            min={1}
            max={3650}
            value={daysValue}
            onChange={(e) => setDays(e.target.value)}
            disabled={!editable}
            aria-invalid={!validDays}
          />
        </Field>
        <div className="flex items-center gap-2">
          <Checkbox
            id="retention-automatic"
            checked={automaticValue}
            onCheckedChange={(checked) => setAutomatic(checked === true)}
            disabled={!editable}
          />
          <Label htmlFor="retention-automatic" className="font-normal">
            期限切れのログを自動で削除する
          </Label>
        </div>
        {editable && (
          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={busy || !validDays}>
              保存
            </Button>
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={() =>
                void run(
                  () => api.post<RetentionRun>("/audit/retention/run", {}),
                  (result) => {
                    const value = result as RetentionRun;
                    return value.more_expired
                      ? `${value.deleted_count}件を削除しました。まだ期限切れのログが残っています。`
                      : `${value.deleted_count}件を削除しました。`;
                  },
                )
              }
            >
              今すぐ期限切れを削除
            </Button>
          </div>
        )}
      </form>
    </Card>
  );
}

export function SettingsPage() {
  const me = useMe();
  return (
    <>
      <PageHeader title="設定" />
      <div className="grid gap-4">
        <ThemeCard />
        <PasswordCard />
        {canReadAuditSettings(me.grants) && <RetentionCard />}
        {canReadAuditSettings(me.grants) && <MonitoringSettingsCard />}
      </div>
    </>
  );
}
