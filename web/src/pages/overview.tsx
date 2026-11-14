import { useCallback } from "react";
import { Link } from "react-router";
import { useMe } from "@/app/session";
import { useLoad } from "@/app/use-load";
import { ErrorText, Loading, PageHeader, Status } from "@/components/common";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { api, type AuditEvent, type Host, type Items } from "@/lib/api";
import { formatTime, grantLabel, roleDescriptions } from "@/lib/format";
import { canReadAudit, canReadHosts } from "@/lib/permissions";

// Counts by the same states the host list shows.
function HostStates({ hosts }: { hosts: Host[] }) {
  const count = (test: (host: Host) => boolean) => hosts.filter(test).length;
  const states = [
    {
      label: "接続中",
      tone: "success" as const,
      value: count((h) => h.state === "active" && h.agent?.connected === true),
    },
    {
      label: "切断",
      tone: "warning" as const,
      value: count((h) => h.state === "active" && h.agent?.connected === false),
    },
    { label: "登録待ち", tone: "neutral" as const, value: count((h) => h.state === "pending") },
    { label: "失効", tone: "destructive" as const, value: count((h) => h.state === "revoked") },
  ].filter((state) => state.value > 0);
  return (
    <ul className="flex flex-wrap gap-x-4 gap-y-1">
      {states.map((state) => (
        <li key={state.label}>
          <Status tone={state.tone} className="text-xs text-muted-foreground">
            {state.label}
            <span className="text-foreground tabular-nums">{state.value}</span>
          </Status>
        </li>
      ))}
    </ul>
  );
}

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
      <div className="grid gap-4 md:grid-cols-3">
        <Card className={readHosts ? "md:col-span-2" : "md:col-span-3"}>
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
                  <span className="text-sm">{grantLabel(grant, data?.hosts)}</span>
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
              <>
                <p className="text-3xl leading-none font-medium tabular-nums">
                  {data?.hosts.length ?? 0}
                </p>
                {data && data.hosts.length > 0 && <HostStates hosts={data.hosts} />}
                <Link to="/hosts" className="link mt-auto w-fit text-sm">
                  ホスト一覧を開く
                </Link>
              </>
            )}
          </Card>
        )}
        {readAudit && (
          <Card className="md:col-span-3">
            <CardHeader>
              <CardTitle>最近のアクティビティ</CardTitle>
            </CardHeader>
            {loading ? (
              <Loading />
            ) : data && data.events.length > 0 ? (
              <ul className="grid gap-2.5">
                {data.events.map((event) => (
                  <li
                    key={event.id}
                    className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm"
                  >
                    <span className="stamp w-36 shrink-0">{formatTime(event.at)}</span>
                    <code className="font-mono text-xs">{event.action}</code>
                    <Status
                      tone={event.outcome === "success" ? "success" : "destructive"}
                      className="ml-auto font-mono text-xs text-muted-foreground"
                    >
                      {event.outcome}
                    </Status>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-sm text-muted-foreground">まだ記録がありません。</p>
            )}
            <Link to="/activity" className="link w-fit text-sm">
              アクティビティをすべて見る
            </Link>
          </Card>
        )}
        <Card className="md:col-span-3">
          <CardHeader>
            <CardTitle>このバージョンについて</CardTitle>
          </CardHeader>
          <p className="max-w-3xl text-sm leading-6 text-muted-foreground">
            AmuOSは開発途中です。ユーザー・グループ・権限の管理、Agentによるホスト情報・監視値の収集と履歴、タスク、監査ログの確認ができます。ファイル管理は、Agentで明示的に公開したディレクトリで利用できます。アプリのデプロイ・コンテナ・VM管理は今後の実装予定です。
          </p>
        </Card>
      </div>
    </>
  );
}
