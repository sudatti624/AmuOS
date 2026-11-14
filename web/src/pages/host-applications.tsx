import { useCallback, useState } from "react";
import { useMe } from "@/app/session";
import { useConfirm } from "@/app/confirm";
import { useLoad } from "@/app/use-load";
import { ErrorText, Field, Loading, SectionHeader } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Input, NativeSelect } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableEmpty,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, errorMessage, type Items, type Role } from "@/lib/api";
import { canManageHost } from "@/lib/permissions";

interface Application {
  name: string;
  revision: number;
  config: {
    runtime: string;
    read_roles: Role[];
    operate_roles: Role[];
  };
}

export function HostApplications({ hostId, mutable }: { hostId: string; mutable: boolean }) {
  const me = useMe();
  const confirm = useConfirm();
  const manager = mutable && canManageHost(me.grants, hostId);
  const base = `/hosts/${hostId}/applications`;
  const list = useLoad(useCallback(() => api.get<Items<Application>>(base), [base]));
  const [selected, setSelected] = useState<Application | null>(null);
  const [name, setName] = useState("");
  const [runtime, setRuntime] = useState("python");
  const [viewer, setViewer] = useState(false);
  const [operator, setOperator] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState("");

  function reset() {
    setSelected(null);
    setName("");
    setRuntime("python");
    setViewer(false);
    setOperator(false);
  }

  async function act(fn: () => Promise<void>) {
    setBusy(true);
    setError(null);
    setStatus("");
    try {
      await fn();
    } catch (failure) {
      setError(
        errorMessage(failure, {
          conflict:
            "登録内容が変更されたか、同じ名前が使用済みです。一覧を更新して確認してください。",
        }),
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="flex flex-col gap-4" aria-label="アプリ登録">
      <SectionHeader
        title="アプリ登録"
        description="Linux Managed Serviceに向けた登録情報です。現在は登録と権限の設定に対応しています。デプロイ・起動・ログ取得はまだ利用できません。"
        actions={
          <Button
            size="sm"
            variant="outline"
            disabled={busy || list.loading}
            onClick={() => {
              reset();
              void list.reload();
            }}
          >
            一覧を更新
          </Button>
        }
      />
      {list.error && <ErrorText>{list.error}</ErrorText>}
      {error && <ErrorText>{error}</ErrorText>}
      {status && (
        <p role="status" className="text-sm text-muted-foreground">
          {status}
        </p>
      )}
      {list.loading ? (
        <Loading />
      ) : (
        list.data && (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>アプリ名</TableHead>
                <TableHead>ランタイム</TableHead>
                <TableHead>版</TableHead>
                <TableHead>閲覧ロール</TableHead>
                {manager && <TableHead className="text-right">操作</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.data.items.length === 0 && (
                <TableEmpty colSpan={manager ? 5 : 4}>
                  表示できるアプリ登録はありません。
                </TableEmpty>
              )}
              {list.data.items.map((app) => (
                <TableRow key={app.name}>
                  <TableCell className="font-mono text-[13px]">{app.name}</TableCell>
                  <TableCell>{app.config.runtime}</TableCell>
                  <TableCell className="tabular-nums">{app.revision}</TableCell>
                  <TableCell>{app.config.read_roles.join(", ") || "管理者のみ"}</TableCell>
                  {manager && (
                    <TableCell className="py-1.5">
                      <div className="flex justify-end gap-2">
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={busy}
                          onClick={() => {
                            setSelected(app);
                            setName(app.name);
                            setRuntime(app.config.runtime);
                            setViewer(app.config.read_roles.includes("Viewer"));
                            setOperator(app.config.read_roles.includes("Operator"));
                            setStatus("");
                          }}
                        >
                          設定
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={busy}
                          onClick={() =>
                            void act(async () => {
                              if (
                                !(await confirm({
                                  title: "アプリ登録を解除しますか",
                                  description: `${app.name} の登録を一覧から除きます。同じ名前は再利用できません。ホストのファイルやプロセスを変更する操作は行いません。`,
                                  confirmLabel: "登録解除",
                                }))
                              )
                                return;
                              await api.delete(`${base}/${app.name}`, {
                                expected_revision: app.revision,
                              });
                              reset();
                              await list.reload();
                              setStatus("アプリ登録を解除しました。");
                            })
                          }
                        >
                          登録解除
                        </Button>
                      </div>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )
      )}
      {manager && (
        <form
          className="max-w-xl rounded-xl border bg-card p-5"
          onSubmit={(event) => {
            event.preventDefault();
            void act(async () => {
              const read: Role[] = ["Admin"];
              if (operator) read.push("Operator");
              if (viewer) read.push("Viewer");
              const saved = await api.put<Application>(`${base}/${name}`, {
                expected_revision: selected?.revision ?? 0,
                config: {
                  runtime,
                  read_roles: read,
                  operate_roles: (selected?.config.operate_roles ?? []).filter((role) =>
                    read.includes(role),
                  ),
                },
              });
              setSelected(saved);
              await list.reload();
              setStatus("アプリ登録を保存しました。");
            });
          }}
        >
          <fieldset disabled={busy} className="grid gap-4">
            <legend className="mb-4 text-base font-medium">
              {selected ? `${selected.name} の設定` : "アプリを登録"}
            </legend>
            <Field id="app-name" label="アプリ名（英小文字で開始）">
              <Input
                id="app-name"
                value={name}
                disabled={selected !== null}
                required
                maxLength={63}
                pattern="[a-z]([a-z0-9-]*[a-z0-9])?"
                onChange={(event) => setName(event.target.value)}
              />
            </Field>
            <Field id="app-runtime" label="予定するランタイム">
              <NativeSelect
                id="app-runtime"
                value={runtime}
                onChange={(event) => setRuntime(event.target.value)}
              >
                <option value="python">Python</option>
                <option value="node">Node.js</option>
                <option value="binary">ビルド済み実行ファイル</option>
              </NativeSelect>
            </Field>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={viewer}
                onChange={(event) => setViewer(event.target.checked)}
              />
              Viewerに登録情報の閲覧を許可
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={operator}
                onChange={(event) => setOperator(event.target.checked)}
              />
              Operatorに登録情報の閲覧を許可
            </label>
            <p className="text-xs text-muted-foreground">AdminとOwnerは登録を管理できます。</p>
            <div className="flex gap-2">
              <Button type="submit">アプリ登録を保存</Button>
              {selected && (
                <Button type="button" variant="outline" onClick={reset}>
                  新しいアプリ
                </Button>
              )}
            </div>
          </fieldset>
        </form>
      )}
    </section>
  );
}
