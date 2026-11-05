import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Link, useParams } from "react-router";
import { ArrowLeftIcon } from "lucide-react";
import { useLoad } from "@/app/use-load";
import { LineChart } from "@/components/chart";
import { ErrorText, Loading, PageHeader } from "@/components/common";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableEmpty,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, type History, type HostDetail, type HostInfo, type Sample } from "@/lib/api";
import {
  formatBytes,
  formatDuration,
  formatPercent,
  formatRate,
  formatStep,
  formatTime,
} from "@/lib/format";
import { AgentStatus } from "@/pages/hosts";

const ranges = [
  { value: "1h", label: "1時間" },
  { value: "6h", label: "6時間" },
  { value: "24h", label: "24時間" },
  { value: "7d", label: "7日" },
  { value: "30d", label: "30日" },
];

const unavailable = <span className="text-muted-foreground">取得できません</span>;

function show<T>(value: T | null | undefined, format: (v: T) => ReactNode) {
  return value === null || value === undefined || value === "" ? unavailable : format(value);
}

function Metric({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1 rounded-lg border bg-card p-4">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-lg font-semibold tabular-nums">{children}</span>
    </div>
  );
}

function CurrentValues({ latest, info }: { latest: Sample; info: HostInfo | null }) {
  const noSwap = info?.swap_bytes === 0;
  return (
    <section className="flex flex-col gap-3" aria-label="現在の値">
      {latest.current ? (
        <p className="text-sm text-muted-foreground">
          {formatTime(latest.collected_at)} に取得した値です。
        </p>
      ) : (
        <p
          role="status"
          className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-sm"
        >
          最新の値ではありません。最後に受信した {formatTime(latest.collected_at)}{" "}
          の値を表示しています。
        </p>
      )}
      <div
        className={`grid gap-3 sm:grid-cols-2 lg:grid-cols-4 ${latest.current ? "" : "opacity-70"}`}
      >
        <Metric label="CPU使用率">{show(latest.cpu, formatPercent)}</Metric>
        <Metric label="メモリ使用率">{show(latest.memory, formatPercent)}</Metric>
        <Metric label="Swap使用率">
          {noSwap ? (
            <span className="text-muted-foreground">Swapなし</span>
          ) : (
            show(latest.swap, formatPercent)
          )}
        </Metric>
        <Metric label="ディスク使用率（/）">{show(latest.disk, formatPercent)}</Metric>
        <Metric label="負荷（1分・5分・15分）">
          {latest.load1 === null
            ? unavailable
            : [latest.load1, latest.load5, latest.load15]
                .map((v) => (v === null ? "—" : v.toFixed(2)))
                .join(" / ")}
        </Metric>
        <Metric label="ディスクI/O（読み・書き）">
          {latest.disk_read === null || latest.disk_write === null
            ? unavailable
            : `${formatRate(latest.disk_read)} / ${formatRate(latest.disk_write)}`}
        </Metric>
        <Metric label="ネットワーク（受信・送信）">
          {latest.net_rx === null || latest.net_tx === null
            ? unavailable
            : `${formatRate(latest.net_rx)} / ${formatRate(latest.net_tx)}`}
        </Metric>
        <Metric label="稼働時間">{show(latest.uptime, formatDuration)}</Metric>
      </div>
    </section>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[9rem_1fr] gap-2 py-1.5 text-sm">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  );
}

function InfoCard({ detail, info }: { detail: HostDetail; info: HostInfo }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>ホスト情報</CardTitle>
        <CardDescription>
          {formatTime(info.collected_at)} に取得、{formatTime(info.received_at)} に受信
        </CardDescription>
      </CardHeader>
      <dl className="divide-y">
        <Row label="ホスト名">{show(info.hostname, (v) => v)}</Row>
        <Row label="OS">{show(info.os, (v) => v)}</Row>
        <Row label="カーネル">{show(info.kernel, (v) => v)}</Row>
        <Row label="アーキテクチャ">{show(info.arch, (v) => v)}</Row>
        <Row label="CPU">
          {info.cpu_model || info.cpu_count
            ? `${info.cpu_model || "型番不明"}${info.cpu_count ? ` × ${info.cpu_count}` : ""}`
            : unavailable}
        </Row>
        <Row label="メモリ">{show(info.memory_bytes, formatBytes)}</Row>
        <Row label="Swap">
          {info.swap_bytes === 0 ? "なし" : show(info.swap_bytes, formatBytes)}
        </Row>
        <Row label="起動時刻">{show(info.boot_time, formatTime)}</Row>
        <Row label="Agent">
          {detail.agent ? `${detail.agent.version || "版不明"}` : unavailable}
        </Row>
        <Row label="対応機能">
          {detail.agent ? detail.agent.capabilities.join("、") || "なし" : unavailable}
        </Row>
      </dl>
    </Card>
  );
}

function Storage({ info }: { info: HostInfo }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>ディスク・ネットワーク</CardTitle>
      </CardHeader>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>マウント先</TableHead>
            <TableHead>種類</TableHead>
            <TableHead className="text-right">使用量</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {info.filesystems.length === 0 && (
            <TableEmpty colSpan={3}>取得できたファイルシステムはありません。</TableEmpty>
          )}
          {info.filesystems.map((f) => (
            <TableRow key={f.mount}>
              <TableCell className="font-mono text-xs">{f.mount}</TableCell>
              <TableCell>{f.type}</TableCell>
              <TableCell className="text-right tabular-nums">
                {f.total_bytes === null || f.used_bytes === null
                  ? unavailable
                  : `${formatBytes(f.used_bytes)} / ${formatBytes(f.total_bytes)}（${formatPercent(
                      f.total_bytes > 0 ? (f.used_bytes / f.total_bytes) * 100 : 0,
                    )}）`}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <dl className="mt-3 divide-y">
        {info.interfaces.length === 0 && <Row label="インターフェース">{unavailable}</Row>}
        {info.interfaces.map((i) => (
          <Row key={i.name} label={i.name}>
            <span className="font-mono text-xs">{i.addresses.join(", ") || "アドレスなし"}</span>
          </Row>
        ))}
      </dl>
    </Card>
  );
}

function Charts({ history }: { history: History }) {
  const s = history.series;
  const common = { times: history.times, step: history.step, from: history.from, to: history.to };
  return (
    <div className="grid gap-3 lg:grid-cols-2">
      <LineChart
        {...common}
        title="CPU使用率"
        max={100}
        format={formatPercent}
        series={[
          { label: "平均", values: s.cpu.avg, color: "var(--primary)" },
          // With one sample per step the maximum equals the average.
          ...(history.step >= 300
            ? [{ label: "最大", values: s.cpu.max, color: "var(--warning)" }]
            : []),
        ]}
      />
      <LineChart
        {...common}
        title="メモリ・Swap使用率"
        max={100}
        format={formatPercent}
        series={[
          { label: "メモリ", values: s.memory.avg, color: "var(--primary)" },
          { label: "Swap", values: s.swap.avg, color: "var(--warning)" },
        ]}
      />
      <LineChart
        {...common}
        title="ディスク使用率（/）"
        max={100}
        format={formatPercent}
        series={[{ label: "使用率", values: s.disk.avg, color: "var(--primary)" }]}
      />
      <LineChart
        {...common}
        title="負荷"
        format={(v) => v.toFixed(1)}
        series={[
          { label: "1分", values: s.load1.avg, color: "var(--primary)" },
          { label: "15分", values: s.load15.avg, color: "var(--warning)" },
        ]}
      />
      <LineChart
        {...common}
        title="ディスクI/O"
        format={formatRate}
        series={[
          { label: "読み", values: s.disk_read.avg, color: "var(--primary)" },
          { label: "書き", values: s.disk_write.avg, color: "var(--warning)" },
        ]}
      />
      <LineChart
        {...common}
        title="ネットワーク"
        format={formatRate}
        series={[
          { label: "受信", values: s.net_rx.avg, color: "var(--primary)" },
          { label: "送信", values: s.net_tx.avg, color: "var(--warning)" },
        ]}
      />
    </div>
  );
}

export function HostDetailPage() {
  const { id = "" } = useParams();
  const [range, setRange] = useState("1h");
  const loadDetail = useCallback(
    () => api.get<HostDetail>(`/hosts/${encodeURIComponent(id)}`),
    [id],
  );
  const loadHistory = useCallback(
    () => api.get<History>(`/hosts/${encodeURIComponent(id)}/metrics?range=${range}`),
    [id, range],
  );
  const detail = useLoad(loadDetail);
  const history = useLoad(loadHistory);
  const { reload: reloadDetail } = detail;
  const { reload: reloadHistory } = history;
  const interval = (detail.data?.sample_interval ?? 15) * 1000;

  useEffect(() => {
    const timer = setInterval(() => {
      if (document.visibilityState === "visible") {
        void reloadDetail();
        void reloadHistory();
      }
    }, interval);
    return () => clearInterval(timer);
  }, [interval, reloadDetail, reloadHistory]);

  const host = detail.data;
  return (
    <>
      <Link
        to="/hosts"
        className="flex w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeftIcon className="size-4" />
        ホスト一覧
      </Link>
      <PageHeader
        title={host?.name ?? "ホスト"}
        description="値は取得時刻とあわせて表示します。取得できなかった値は0ではなく「取得できません」と表示します。"
        actions={host ? <AgentStatus host={host} /> : undefined}
      />
      {detail.error && <ErrorText>{detail.error}</ErrorText>}
      {!host && detail.loading && <Loading />}
      {host && (
        <>
          {!host.latest && !host.info && (
            <p className="rounded-md border px-3 py-6 text-center text-sm text-muted-foreground">
              {host.state === "pending"
                ? "Agentを登録すると、ここにホスト情報と監視値が表示されます。"
                : "まだ監視値を受信していません。"}
            </p>
          )}
          {host.latest && <CurrentValues latest={host.latest} info={host.info} />}
          {host.info && (
            <div className="grid gap-3 lg:grid-cols-2">
              <InfoCard detail={host} info={host.info} />
              <Storage info={host.info} />
            </div>
          )}
          <section className="flex flex-col gap-3" aria-label="履歴">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h2 className="text-base font-semibold">履歴</h2>
              <Tabs value={range} onValueChange={setRange}>
                <TabsList>
                  {ranges.map((r) => (
                    <TabsTrigger key={r.value} value={r.value}>
                      {r.label}
                    </TabsTrigger>
                  ))}
                </TabsList>
              </Tabs>
            </div>
            {history.data && (
              <p className="text-xs text-muted-foreground">
                1点は{formatStep(history.data.step)}
                ごとの平均です。線が途切れている区間は、データがない期間です（Agentの停止や通信断など）。0として補いません。
              </p>
            )}
            {history.error && <ErrorText>{history.error}</ErrorText>}
            {history.data ? <Charts history={history.data} /> : history.loading && <Loading />}
          </section>
        </>
      )}
    </>
  );
}
