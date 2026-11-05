import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { RefreshCwIcon, RotateCcwIcon } from "lucide-react";
import { useConfirm } from "@/app/confirm";
import { useMe } from "@/app/session";
import { useNotify } from "@/app/notifications";
import { useLoad } from "@/app/use-load";
import { ErrorText, Field } from "@/components/common";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableEmpty,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, errorMessage, type Host, type Items, type Task, type TaskState } from "@/lib/api";
import { formatTime, shortId } from "@/lib/format";
import { canCollect, canManageHost } from "@/lib/permissions";

const states: Record<
  TaskState,
  { label: string; variant: "default" | "secondary" | "success" | "warning" | "destructive" }
> = {
  queued: { label: "待機中", variant: "secondary" },
  dispatching: { label: "配送中", variant: "default" },
  running: { label: "実行中", variant: "default" },
  succeeded: { label: "成功", variant: "success" },
  failed: { label: "失敗", variant: "destructive" },
  timed_out: { label: "期限切れ", variant: "warning" },
  cancelled: { label: "取消", variant: "secondary" },
  unknown: { label: "結果不明", variant: "warning" },
};

const reasons: Record<string, string> = {
  not_dispatched: "受付期限までにAgentが受け取りませんでした",
  deadline_passed: "実行期限までに結果が届きませんでした",
  cancelled_by_user: "取り消されました",
  resolved_manually: "確認した根拠をもとに結果を確定しました",
  permission_revoked: "依頼した人の権限がなくなったため、配送しませんでした",
  host_revoked: "ホストが失効しました",
  host_deleted: "ホストが削除されました",
  control_plane_restarted: "Control Planeの再起動で結果が分からなくなりました",
  restored: "バックアップから復元したため、結果を確かめられません",
};

const open = new Set<TaskState>(["queued", "dispatching", "running"]);

const retryMessages = {
  conflict:
    "このタスクは再実行できません。未完了か、すでに再実行済みか、再実行の上限（3回）に達しています。",
};

function ResolveDialog({ task, onClose }: { task: Task; onClose: (changed: boolean) => void }) {
  const notify = useNotify();
  const [resolution, setResolution] = useState<"succeeded" | "failed">("failed");
  const [evidence, setEvidence] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.post(`/tasks/${task.id}/resolve`, { resolution, evidence: evidence.trim() });
      notify.success("タスクの結果を確定しました。");
      onClose(true);
    } catch (failure) {
      setError(errorMessage(failure));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(next) => !next && onClose(false)}>
      <DialogContent>
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>結果不明のタスクを確定</DialogTitle>
            <DialogDescription>
              ホストで実際の状態を確かめたうえで、結果と確認した根拠を記録します。根拠は監査ログに残ります。タスクを再実行することはありません。
            </DialogDescription>
          </DialogHeader>
          <fieldset className="grid gap-2">
            <legend className="mb-1 text-sm font-medium">結果</legend>
            {(["failed", "succeeded"] as const).map((value) => (
              <label key={value} className="flex items-center gap-2 text-sm">
                <input
                  type="radio"
                  name="resolution"
                  value={value}
                  checked={resolution === value}
                  onChange={() => setResolution(value)}
                />
                {states[value].label}
              </label>
            ))}
          </fieldset>
          <Field
            id="evidence"
            label="確認した根拠"
            hint="1〜500バイト。秘密の情報は書かないでください。"
          >
            <Input
              id="evidence"
              value={evidence}
              maxLength={500}
              onChange={(e) => setEvidence(e.target.value)}
              required
              autoFocus
            />
          </Field>
          {error && <ErrorText>{error}</ErrorText>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onClose(false)}>
              キャンセル
            </Button>
            <Button type="submit" disabled={busy || evidence.trim() === ""}>
              確定する
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function HostTasks({ host, onCollected }: { host: Host; onCollected: () => void }) {
  const me = useMe();
  const notify = useNotify();
  const confirm = useConfirm();
  const load = useCallback(
    () => api.get<Items<Task>>(`/hosts/${encodeURIComponent(host.id)}/tasks`).then((r) => r.items),
    [host.id],
  );
  const { data, error, reload } = useLoad(load);
  const [busy, setBusy] = useState(false);
  const [resolving, setResolving] = useState<Task | null>(null);
  // One key per intended action: resending after a lost response reuses it,
  // so the server returns the first task instead of creating another.
  const keys = useRef(new Map<string, string>());
  const keyFor = (action: string) => {
    let key = keys.current.get(action);
    if (!key) {
      key = crypto.randomUUID();
      keys.current.set(action, key);
    }
    return key;
  };
  const collector = canCollect(me.grants, host.id);
  const manager = canManageHost(me.grants, host.id);
  const pending = data?.some((t) => open.has(t.state)) ?? false;
  const previous = useRef(pending);

  // Follow open tasks closely; when one finishes, the host values changed.
  useEffect(() => {
    if (previous.current && !pending) onCollected();
    previous.current = pending;
    if (!pending) return;
    const timer = setInterval(() => void reload(), 2000);
    return () => clearInterval(timer);
  }, [pending, reload, onCollected]);

  const create = async (action: string, request: () => Promise<Task>, success: string) => {
    setBusy(true);
    try {
      await request();
      keys.current.delete(action);
      notify.success(success);
    } catch (failure) {
      notify.error(errorMessage(failure, action === "collect" ? {} : retryMessages));
    } finally {
      setBusy(false);
      void reload();
    }
  };

  const collect = () =>
    create(
      "collect",
      () =>
        api.post<Task>(
          `/hosts/${host.id}/tasks`,
          { operation: "collect" },
          { idempotencyKey: keyFor("collect") },
        ),
      "収集を依頼しました。Agentが受け取ると実行されます。",
    );

  const retry = (task: Task) =>
    create(
      `retry:${task.id}`,
      () =>
        api.post<Task>(
          `/tasks/${task.id}/retry`,
          {},
          { idempotencyKey: keyFor(`retry:${task.id}`) },
        ),
      "新しいタスクとして再実行を依頼しました。",
    );

  const cancel = async (task: Task) => {
    const ok = await confirm({
      title: "タスクを取り消しますか？",
      description: "Agentがまだ受け取っていないタスクだけを取り消せます。",
      confirmLabel: "取り消す",
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.post(`/tasks/${task.id}/cancel`, {});
      notify.success("タスクを取り消しました。");
    } catch (failure) {
      notify.error(
        errorMessage(failure, {
          conflict: "Agentがすでに受け取ったため、取り消せませんでした。",
        }),
      );
    }
    void reload();
  };

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <CardTitle>タスク</CardTitle>
          <CardDescription>
            「今すぐ収集」は、ホスト情報・監視値・サービスの状態をその場で取り直します。読み取りだけで、ホストを変更しません。
          </CardDescription>
        </div>
        {collector && (
          <Button onClick={() => void collect()} disabled={busy || host.state !== "active"}>
            <RefreshCwIcon />
            今すぐ収集
          </Button>
        )}
      </CardHeader>
      {collector && host.state !== "active" && (
        <p className="text-sm text-muted-foreground">
          Agentが登録されているホストでだけ収集を依頼できます。
        </p>
      )}
      {error && <ErrorText>{error}</ErrorText>}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>依頼日時</TableHead>
            <TableHead>状態</TableHead>
            <TableHead>詳細</TableHead>
            {(collector || manager) && <TableHead className="text-right">操作</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {data && data.length === 0 && (
            <TableEmpty colSpan={collector || manager ? 4 : 3}>まだタスクはありません。</TableEmpty>
          )}
          {data?.map((task) => {
            const state = states[task.state];
            const retryable = !open.has(task.state) && !task.retried_by;
            return (
              <TableRow key={task.id}>
                <TableCell className="tabular-nums">{formatTime(task.created_at)}</TableCell>
                <TableCell>
                  <Badge variant={state.variant}>{state.label}</Badge>
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  <div className="flex flex-col gap-0.5">
                    {task.reason && <span>{reasons[task.reason] ?? task.reason}</span>}
                    {task.retry_of && <span>再実行（元: {shortId(task.retry_of)}）</span>}
                    {task.retried_by && <span>再実行済み（{shortId(task.retried_by)}）</span>}
                    {task.finished_at && <span>終了 {formatTime(task.finished_at)}</span>}
                  </div>
                </TableCell>
                {(collector || manager) && (
                  <TableCell>
                    <div className="flex justify-end gap-2">
                      {collector && task.state === "queued" && (
                        <Button size="sm" variant="outline" onClick={() => void cancel(task)}>
                          取消
                        </Button>
                      )}
                      {manager && task.state === "unknown" && (
                        <Button size="sm" variant="outline" onClick={() => setResolving(task)}>
                          結果を確定
                        </Button>
                      )}
                      {collector && retryable && host.state === "active" && (
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={busy}
                          onClick={() => void retry(task)}
                        >
                          <RotateCcwIcon />
                          再実行
                        </Button>
                      )}
                    </div>
                  </TableCell>
                )}
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
      {resolving && (
        <ResolveDialog
          task={resolving}
          onClose={(changed) => {
            setResolving(null);
            if (changed) void reload();
          }}
        />
      )}
    </Card>
  );
}
