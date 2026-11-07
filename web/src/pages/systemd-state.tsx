import { useCallback } from "react";
import { useLoad } from "@/app/use-load";
import { api, errorMessage } from "@/lib/api";
import { ErrorText } from "@/components/common";
interface Snapshot {
  collected_at: number;
  received_at: number;
  available: boolean;
  truncated: boolean;
  units: { unit: string; load: string; active: string; sub: string }[];
}
export function SystemdState({ hostId, windows = false }: { hostId: string; windows?: boolean }) {
  const load = useCallback(() => api.get<Snapshot | null>(`/hosts/${hostId}/systemd`), [hostId]);
  const { data, error } = useLoad(load);
  return (
    <section aria-label="systemd" className="rounded-md border p-4">
      <h2 className="font-semibold">サービスの状態</h2>
      {windows && <p>Windowsではsystemdの状態取得に対応していません。</p>}
      {error && <ErrorText>{errorMessage(error)}</ErrorText>}
      {!windows && !data && <p>まだ状態を受信していません。</p>}
      {!windows && data && (
        <>
          <p className="text-sm text-muted-foreground">
            取得: {new Date(data.collected_at * 1000).toLocaleString()} / 受信:{" "}
            {new Date(data.received_at * 1000).toLocaleString()}。最後に受信した状態です。
          </p>
          {!data.available ? (
            <p>状態を取得できません。</p>
          ) : (
            <div className="max-h-96 overflow-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr>
                    <th>サービス</th>
                    <th>読込み</th>
                    <th>稼働状態</th>
                    <th>詳細状態</th>
                  </tr>
                </thead>
                <tbody>
                  {data.units.map((unit) => (
                    <tr key={unit.unit}>
                      <td>{unit.unit}</td>
                      <td>{unit.load}</td>
                      <td>{unit.active}</td>
                      <td>{unit.sub}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {data.truncated && <p>先頭256件を表示しています。</p>}
        </>
      )}
    </section>
  );
}
