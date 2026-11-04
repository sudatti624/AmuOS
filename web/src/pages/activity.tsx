import { useCallback } from "react";
import { RefreshCwIcon } from "lucide-react";
import { useMe } from "@/app/session";
import { useLoad } from "@/app/use-load";
import { ErrorText, Loading, PageHeader } from "@/components/common";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableEmpty,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, type AuditEvent, type Items, type User } from "@/lib/api";
import { formatTime, shortId } from "@/lib/format";
import { canManageIdentity } from "@/lib/permissions";

function hasDetails(details: unknown) {
  return typeof details === "object" && details !== null && Object.keys(details).length > 0;
}

export function ActivityPage() {
  const me = useMe();
  const resolveNames = canManageIdentity(me.grants);
  const load = useCallback(async () => {
    const [events, users] = await Promise.all([
      api.get<Items<AuditEvent>>("/audit").then((r) => r.items),
      resolveNames ? api.get<Items<User>>("/users").then((r) => r.items) : Promise.resolve([]),
    ]);
    const names = new Map(users.map((u) => [u.id, u.username]));
    names.set(me.id, me.username);
    return { events, names };
  }, [resolveNames, me.id, me.username]);
  const { data, error, loading, reload } = useLoad(load);
  const name = (id: string) =>
    id === ""
      ? "—"
      : (data?.names.get(id) ?? (id === "system" || id === "anonymous" ? id : shortId(id)));

  return (
    <>
      <PageHeader
        title="アクティビティ"
        description="監査ログのうち、閲覧できる範囲の最新100件です。"
        actions={
          <Button variant="outline" onClick={() => void reload()} disabled={loading}>
            <RefreshCwIcon />
            更新
          </Button>
        }
      />
      {error && <ErrorText>{error}</ErrorText>}
      {loading && !data ? (
        <Loading />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>日時</TableHead>
              <TableHead>操作</TableHead>
              <TableHead>実行者</TableHead>
              <TableHead>対象</TableHead>
              <TableHead>結果</TableHead>
              <TableHead>詳細</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data && data.events.length === 0 && (
              <TableEmpty colSpan={6}>表示できる記録はありません。</TableEmpty>
            )}
            {data?.events.map((event) => (
              <TableRow key={event.id} className="align-top">
                <TableCell className="whitespace-nowrap tabular-nums">
                  {formatTime(event.at)}
                </TableCell>
                <TableCell>
                  <code className="font-mono text-xs">{event.action}</code>
                </TableCell>
                <TableCell>{name(event.actor_id)}</TableCell>
                <TableCell>{name(event.target_id)}</TableCell>
                <TableCell>
                  <div className="flex flex-col gap-1">
                    <Badge variant={event.outcome === "success" ? "success" : "destructive"}>
                      {event.outcome}
                    </Badge>
                    {event.reason && (
                      <span className="text-xs text-muted-foreground">{event.reason}</span>
                    )}
                  </div>
                </TableCell>
                <TableCell className="min-w-48">
                  {hasDetails(event.details) || event.request_id ? (
                    <details>
                      <summary className="cursor-pointer text-xs text-primary">表示</summary>
                      <div className="mt-2 flex flex-col gap-1">
                        {event.request_id && (
                          <span className="text-xs text-muted-foreground">
                            リクエストID: <code className="font-mono">{event.request_id}</code>
                          </span>
                        )}
                        {hasDetails(event.details) && (
                          <pre className="max-w-md overflow-x-auto rounded-md bg-muted p-2 font-mono text-xs">
                            {JSON.stringify(event.details, null, 2)}
                          </pre>
                        )}
                      </div>
                    </details>
                  ) : (
                    <span className="text-xs text-muted-foreground">—</span>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </>
  );
}
