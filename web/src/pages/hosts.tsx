import { useCallback, useState, type FormEvent } from "react";
import { CopyIcon, KeyRoundIcon, PlusIcon, ShieldOffIcon, Trash2Icon } from "lucide-react";
import { useConfirm } from "@/app/confirm";
import { useMe } from "@/app/session";
import { useNotify } from "@/app/notifications";
import { useLoad } from "@/app/use-load";
import { ErrorText, Field, Loading, PageHeader } from "@/components/common";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
import { api, errorMessage, type Enrollment, type Host, type Items } from "@/lib/api";
import { formatTime } from "@/lib/format";
import { canCreateHosts, canManageHost } from "@/lib/permissions";

function newKey() {
  return crypto.randomUUID();
}

function CreateHostDialog({ onCreated }: { onCreated: () => void }) {
  const notify = useNotify();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  // One key per attempt: resending after a network failure reuses it, so the
  // server returns the first result instead of creating a second host.
  const [key, setKey] = useState(newKey);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const reset = (next: boolean) => {
    setOpen(next);
    if (next) {
      setName("");
      setError(null);
      setKey(newKey());
    }
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const host = await api.post<Host>("/hosts", { name: name.trim() }, { idempotencyKey: key });
      notify.success(`ホスト「${host.name}」の登録枠を作成しました。`);
      setOpen(false);
      onCreated();
    } catch (failure) {
      setError(
        errorMessage(failure, {
          conflict: "同じ要求がすでに別の内容で処理されています。画面を閉じてやり直してください。",
        }),
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={reset}>
      <Button onClick={() => reset(true)}>
        <PlusIcon />
        登録枠を追加
      </Button>
      <DialogContent>
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>ホストの登録枠を追加</DialogTitle>
            <DialogDescription>
              ホストのIDを先に確保します。追加したあと、そのホストのAgentに渡す登録コードを発行できます。
            </DialogDescription>
          </DialogHeader>
          <Field id="host-name" label="表示名" hint="1〜128文字">
            <Input
              id="host-name"
              value={name}
              maxLength={128}
              onChange={(e) => setName(e.target.value)}
              required
              autoFocus
            />
          </Field>
          {error && <ErrorText>{error}</ErrorText>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              キャンセル
            </Button>
            <Button type="submit" disabled={busy || name.trim() === ""}>
              追加
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function EnrollmentDialog({ host, onClose }: { host: Host; onClose: () => void }) {
  const notify = useNotify();
  const [enrollment, setEnrollment] = useState<Enrollment | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const issue = async () => {
    setBusy(true);
    setError(null);
    try {
      setEnrollment(
        await api.post<Enrollment>(`/hosts/${host.id}/enrollment`, { ttl_minutes: 10 }),
      );
    } catch (failure) {
      setError(
        errorMessage(failure, {
          conflict: "このホストはすでに登録済みか、失効しています。",
        }),
      );
    } finally {
      setBusy(false);
    }
  };

  const copy = async () => {
    if (!enrollment) return;
    try {
      await navigator.clipboard.writeText(enrollment.code);
      notify.success("登録コードをコピーしました。");
    } catch {
      notify.error("コピーできませんでした。コードを選択してコピーしてください。");
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>「{host.name}」の登録コード</DialogTitle>
          <DialogDescription>
            このホストのAgentに一度だけ使えるコードを発行します。有効期限は10分です。新しく発行すると、まだ使っていない以前のコードは使えなくなります。
          </DialogDescription>
        </DialogHeader>
        {enrollment ? (
          <div className="grid gap-3">
            <p className="text-sm">
              このコードはこの画面でしか表示されません。管理対象のホストで、所有者だけが読めるファイルに保存してAgentに渡してください。
            </p>
            <code
              data-testid="enrollment-code"
              className="block rounded-md border bg-muted p-3 font-mono text-xs break-all"
            >
              {enrollment.code}
            </code>
            <p className="text-xs text-muted-foreground">
              有効期限: {formatTime(enrollment.expires_at)}
            </p>
          </div>
        ) : (
          error && <ErrorText>{error}</ErrorText>
        )}
        <DialogFooter>
          {enrollment ? (
            <>
              <Button variant="outline" onClick={() => void copy()}>
                <CopyIcon />
                コピー
              </Button>
              <Button onClick={onClose}>完了</Button>
            </>
          ) : (
            <>
              <Button variant="outline" onClick={onClose}>
                キャンセル
              </Button>
              <Button onClick={() => void issue()} disabled={busy}>
                発行する
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function AgentStatus({ host }: { host: Host }) {
  if (host.state === "pending") return <Badge variant="warning">登録待ち</Badge>;
  if (host.state === "revoked") return <Badge variant="destructive">失効</Badge>;
  const agent = host.agent;
  if (!agent) return <Badge variant="secondary">{host.state}</Badge>;
  return (
    <div className="flex flex-col items-start gap-1">
      {agent.connected ? (
        <Badge variant="success">接続中</Badge>
      ) : (
        <Badge variant="warning">切断</Badge>
      )}
      <span className="text-xs text-muted-foreground">
        {agent.last_seen ? `最終受信 ${formatTime(agent.last_seen)}` : "まだ受信していません"}
        {agent.version && ` ・ Agent ${agent.version}`}
      </span>
      {agent.credential_renewal_due && (
        <span className="text-xs text-warning">
          資格情報の期限が近づいています（{formatTime(agent.credential_expires_at)}）
        </span>
      )}
    </div>
  );
}

export function HostsPage() {
  const me = useMe();
  const notify = useNotify();
  const confirm = useConfirm();
  const load = useCallback(() => api.get<Items<Host>>("/hosts").then((r) => r.items), []);
  const { data, error, loading, reload } = useLoad(load);
  const [enrolling, setEnrolling] = useState<Host | null>(null);
  const anyManageable = data?.some((h) => canManageHost(me.grants, h.id)) ?? false;

  const revoke = async (host: Host) => {
    const ok = await confirm({
      title: `「${host.name}」を失効しますか？`,
      description:
        "このホストのAgentの資格情報と、未使用の登録コードをすべて無効にします。ホストと履歴は残ります。失効したホストに、同じAgentを戻すことはできません。",
      confirmLabel: "失効する",
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.post(`/hosts/${host.id}/revoke`, undefined);
      notify.success(`「${host.name}」を失効しました。`);
    } catch (failure) {
      notify.error(errorMessage(failure));
    }
    void reload();
  };

  const remove = async (host: Host) => {
    const ok = await confirm({
      title: `「${host.name}」を削除しますか？`,
      description:
        "Agentの資格情報を無効にし、ホストを一覧から外します。監査ログは残ります。この操作は取り消せません。",
      confirmLabel: "削除する",
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.delete(`/hosts/${host.id}`);
      notify.success(`「${host.name}」を削除しました。`);
    } catch (failure) {
      notify.error(errorMessage(failure));
    }
    void reload();
  };

  return (
    <>
      <PageHeader
        title="ホスト"
        description="閲覧権限のあるホストだけが表示されます。最終受信から60秒を過ぎたAgentは切断として表示します。"
        actions={
          canCreateHosts(me.grants) ? (
            <CreateHostDialog onCreated={() => void reload()} />
          ) : undefined
        }
      />
      {error && <ErrorText>{error}</ErrorText>}
      {loading && !data ? (
        <Loading />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>表示名</TableHead>
              <TableHead>Agent</TableHead>
              <TableHead>ID</TableHead>
              {anyManageable && <TableHead className="text-right">操作</TableHead>}
            </TableRow>
          </TableHeader>
          <TableBody>
            {data && data.length === 0 && (
              <TableEmpty colSpan={anyManageable ? 4 : 3}>
                表示できるホストはありません。
              </TableEmpty>
            )}
            {data?.map((host) => {
              const manageable = canManageHost(me.grants, host.id);
              return (
                <TableRow key={host.id}>
                  <TableCell className="font-medium">{host.name}</TableCell>
                  <TableCell>
                    <AgentStatus host={host} />
                  </TableCell>
                  <TableCell>
                    <code className="font-mono text-xs text-muted-foreground">{host.id}</code>
                  </TableCell>
                  {anyManageable && (
                    <TableCell>
                      {manageable && (
                        <div className="flex justify-end gap-2">
                          {host.state === "pending" && (
                            <Button size="sm" variant="outline" onClick={() => setEnrolling(host)}>
                              <KeyRoundIcon />
                              登録コード
                            </Button>
                          )}
                          {host.state === "active" && (
                            <Button size="sm" variant="outline" onClick={() => void revoke(host)}>
                              <ShieldOffIcon />
                              失効
                            </Button>
                          )}
                          <Button
                            size="sm"
                            variant="ghost"
                            aria-label={`${host.name}を削除`}
                            onClick={() => void remove(host)}
                          >
                            <Trash2Icon />
                          </Button>
                        </div>
                      )}
                    </TableCell>
                  )}
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      )}
      {enrolling && (
        <EnrollmentDialog
          host={enrolling}
          onClose={() => {
            setEnrolling(null);
            void reload();
          }}
        />
      )}
    </>
  );
}
