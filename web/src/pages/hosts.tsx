import { useCallback, useState, type FormEvent } from "react";
import { PlusIcon } from "lucide-react";
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
import { api, errorMessage, type Host, type Items } from "@/lib/api";
import { canCreateHosts } from "@/lib/permissions";

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
              ホストのIDを先に確保します。Agentの登録と監視は今後のバージョンで対応予定のため、追加したホストは「登録待ち」のままになります。
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

export function HostsPage() {
  const me = useMe();
  const load = useCallback(() => api.get<Items<Host>>("/hosts").then((r) => r.items), []);
  const { data, error, loading, reload } = useLoad(load);

  return (
    <>
      <PageHeader
        title="ホスト"
        description="閲覧権限のあるホストだけが表示されます。"
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
              <TableHead>状態</TableHead>
              <TableHead>ID</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data && data.length === 0 && (
              <TableEmpty colSpan={3}>表示できるホストはありません。</TableEmpty>
            )}
            {data?.map((host) => (
              <TableRow key={host.id}>
                <TableCell className="font-medium">{host.name}</TableCell>
                <TableCell>
                  {host.state === "pending" ? (
                    <Badge variant="warning">登録待ち（Agent未接続）</Badge>
                  ) : (
                    <Badge variant="secondary">{host.state}</Badge>
                  )}
                </TableCell>
                <TableCell>
                  <code className="font-mono text-xs text-muted-foreground">{host.id}</code>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </>
  );
}
