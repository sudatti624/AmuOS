import { useCallback } from "react";
import { Link } from "react-router";
import { useMe } from "@/app/session";
import { useLoad } from "@/app/use-load";
import { ErrorText, Loading, PageHeader } from "@/components/common";
import { Badge } from "@/components/ui/badge";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { api, type AuditEvent, type Host, type Items } from "@/lib/api";
import { formatTime, grantLabel, roleDescriptions } from "@/lib/format";
import { canReadAudit, canReadHosts } from "@/lib/permissions";

export function OverviewPage() {
  const me = useMe();
  const readHosts = canReadHosts(me.grants);
  const readAudit = canReadAudit(me.grants);
  const load = useCallback(async () => {
    const [hosts, events] = await Promise.all([
      readHosts ? api.get<Items<Host>>("/hosts").then((r) => r.items) : Promise.resolve([]),
      readAudit
        ? api.get<Items<AuditEvent>>("/audit").then((r) => r.items.slice(0, 5))
        : Promise.resolve([]),
    ]);
    return { hosts, events };
  }, [readHosts, readAudit]);
  const { data, error, loading } = useLoad(load);

  return (
    <>
      <PageHeader title="概要" description={`${me.username} としてログインしています。`} />
      {error && <ErrorText>{error}</ErrorText>}
      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>自分の権限</CardTitle>
            <CardDescription>
              操作できる内容は、割り当てられたロールと範囲で決まります。
            </CardDescription>
          </CardHeader>
          {me.grants.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              ロールが割り当てられていません。管理者に確認してください。
            </p>
          ) : (
            <ul className="grid gap-2">
              {me.grants.map((grant, index) => (
                <li key={index} className="flex flex-col gap-0.5">
                  <span className="text-sm font-medium">{grantLabel(grant, data?.hosts)}</span>
                  <span className="text-xs text-muted-foreground">
                    {roleDescriptions[grant.role]}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </Card>
        {readHosts && (
          <Card>
            <CardHeader>
              <CardTitle>ホスト</CardTitle>
              <CardDescription>閲覧できるホストの数です。</CardDescription>
            </CardHeader>
            {loading ? (
              <Loading />
            ) : (
              <div className="flex items-end justify-between">
                <p className="text-3xl font-semibold tabular-nums">{data?.hosts.length ?? 0}</p>
                <Link
                  to="/hosts"
                  className="text-sm text-primary underline-offset-4 hover:underline"
                >
                  ホスト一覧を開く
                </Link>
              </div>
            )}
          </Card>
        )}
        {readAudit && (
          <Card className="md:col-span-2">
            <CardHeader>
              <CardTitle>最近のアクティビティ</CardTitle>
            </CardHeader>
            {loading ? (
              <Loading />
            ) : data && data.events.length > 0 ? (
              <ul className="grid gap-2">
                {data.events.map((event) => (
                  <li
                    key={event.id}
                    className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm"
                  >
                    <span className="text-muted-foreground tabular-nums">
                      {formatTime(event.at)}
                    </span>
                    <code className="font-mono text-xs">{event.action}</code>
                    <Badge variant={event.outcome === "success" ? "success" : "destructive"}>
                      {event.outcome}
                    </Badge>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-sm text-muted-foreground">まだ記録がありません。</p>
            )}
            <Link
              to="/activity"
              className="text-sm text-primary underline-offset-4 hover:underline"
            >
              アクティビティをすべて見る
            </Link>
          </Card>
        )}
        <Card className="md:col-span-2">
          <CardHeader>
            <CardTitle>このバージョンについて</CardTitle>
          </CardHeader>
          <p className="text-sm text-muted-foreground">
            AmuOSは開発途中です。ユーザー・グループ・権限の管理、Agentによるホスト情報・監視値の収集と履歴、タスク、監査ログの確認ができます。ファイル管理は、Agentで明示的に公開したディレクトリで利用できます。アプリのデプロイ・コンテナ・VM管理は今後の実装予定です。
          </p>
        </Card>
      </div>
    </>
  );
}
