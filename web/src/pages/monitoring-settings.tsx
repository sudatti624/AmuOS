import { useCallback, useState, type FormEvent } from "react";
import { useLoad } from "@/app/use-load";
import { useNotify } from "@/app/notifications";
import { ErrorText, Field, Loading } from "@/components/common";
import { Card, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, errorMessage } from "@/lib/api";

interface Policy {
  sample_interval_seconds: number;
  raw_retention_days: number;
  rollup_retention_days: number;
}

function PolicyForm({ initial }: { initial: Policy }) {
  const [value, setValue] = useState(initial);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const notify = useNotify();
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (value.rollup_retention_days < value.raw_retention_days) {
      setError("集約データの保持期間は、生データ以上にしてください。");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.put("/monitoring/settings", value);
      notify.success("監視の設定を更新しました。");
    } catch (failure) {
      setError(errorMessage(failure));
    } finally {
      setBusy(false);
    }
  };
  const fields: { key: keyof Policy; label: string; min: number; max: number }[] = [
    { key: "sample_interval_seconds", label: "収集間隔（秒）", min: 5, max: 300 },
    { key: "raw_retention_days", label: "生データの保持期間（日）", min: 1, max: 30 },
    { key: "rollup_retention_days", label: "集約データの保持期間（日）", min: 7, max: 400 },
  ];
  return (
    <form onSubmit={submit} className="grid max-w-sm gap-4">
      {fields.map(({ key, label, min, max }) => (
        <Field key={key} id={key} label={label} hint={`${min}〜${max}`}>
          <Input
            id={key}
            type="number"
            min={min}
            max={max}
            step={1}
            required
            value={value[key]}
            onChange={(event) => setValue({ ...value, [key]: event.target.valueAsNumber })}
          />
        </Field>
      ))}
      {error && <ErrorText>{error}</ErrorText>}
      <Button type="submit" disabled={busy}>
        監視設定を保存
      </Button>
    </form>
  );
}

export function MonitoringSettingsCard() {
  const load = useCallback(() => api.get<Policy>("/monitoring/settings"), []);
  const { data, error, loading } = useLoad(load);
  return (
    <Card>
      <CardHeader>
        <CardTitle>監視</CardTitle>
        <CardDescription>
          全ホストに適用します。収集間隔は次のハートビートから反映され、期限切れデータは定期処理で削除されます。保持期間を延ばしても削除済みデータは戻りません。
        </CardDescription>
      </CardHeader>
      {loading && <Loading />}
      {error && <ErrorText>{errorMessage(error)}</ErrorText>}
      {data && <PolicyForm initial={data} />}
    </Card>
  );
}
