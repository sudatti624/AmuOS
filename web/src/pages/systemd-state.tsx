import { useCallback } from "react";
import { useLoad } from "@/app/use-load";
import { ErrorText, SectionHeader, Status } from "@/components/common";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, errorMessage } from "@/lib/api";
import { formatTime } from "@/lib/format";

interface Snapshot {
  collected_at: number;
  received_at: number;
  available: boolean;
  truncated: boolean;
  units: { unit: string; load: string; active: string; sub: string }[];
}

function tone(active: string) {
  if (active === "active") return "success" as const;
  if (active === "failed") return "destructive" as const;
  if (active === "activating" || active === "deactivating" || active === "reloading")
    return "warning" as const;
  return "neutral" as const;
}

export function SystemdState({ hostId, windows = false }: { hostId: string; windows?: boolean }) {
  const load = useCallback(() => api.get<Snapshot | null>(`/hosts/${hostId}/systemd`), [hostId]);
  const { data, error } = useLoad(load);
  const note = "text-sm text-muted-foreground";
  return (
    <section aria-label="systemd" className="flex flex-col gap-4">
      <SectionHeader title="サービスの状態" />
      {windows && <p className={note}>Windowsではsystemdの状態取得に対応していません。</p>}
      {error && <ErrorText>{errorMessage(error)}</ErrorText>}
      {!windows && !data && <p className={note}>まだ状態を受信していません。</p>}
      {!windows && data && (
        <>
          <p className="stamp">
            {formatTime(data.collected_at)} に取得、{formatTime(data.received_at)}{" "}
            に受信。最後に受信した状態です。
          </p>
          {!data.available ? (
            <p className={note}>状態を取得できません。</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>サービス</TableHead>
                  <TableHead>読込み</TableHead>
                  <TableHead>稼働状態</TableHead>
                  <TableHead>詳細状態</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.units.map((unit) => (
                  <TableRow key={unit.unit}>
                    <TableCell className="font-mono text-[13px]">{unit.unit}</TableCell>
                    <TableCell className="text-muted-foreground">{unit.load}</TableCell>
                    <TableCell>
                      <Status tone={tone(unit.active)}>{unit.active}</Status>
                    </TableCell>
                    <TableCell className="text-muted-foreground">{unit.sub}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          {data.truncated && <p className={note}>先頭256件を表示しています。</p>}
        </>
      )}
    </section>
  );
}
