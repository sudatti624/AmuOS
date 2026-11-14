import { useEffect, useRef, useState } from "react";
import { useMe } from "@/app/session";
import { useConfirm } from "@/app/confirm";
import { FileIcon, FolderIcon } from "lucide-react";
import { ErrorText, Field, Notice, SectionHeader } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Input, NativeSelect, Textarea } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableEmpty,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, errorMessage, type Items, type Role, type Task } from "@/lib/api";
import { canManageHost, isOwner } from "@/lib/permissions";
import { formatBytes, formatTime } from "@/lib/format";

interface Root {
  alias: string;
  read_roles: Role[];
  write_roles: Role[];
}
interface Entry {
  name: string;
  directory: boolean;
  size: number;
  modified: number;
  etag: string;
}
interface Result {
  entries?: Entry[];
  info?: Entry;
  data?: string;
  error?: string;
}
interface FileRequest {
  root: string;
  action: string;
  path: string;
  destination?: string;
  etag?: string;
  confirm?: boolean;
  data?: string;
}
const limit = 256 * 1024;
function encode(bytes: Uint8Array) {
  let text = "";
  for (const b of bytes) text += String.fromCharCode(b);
  return btoa(text);
}
function decode(text: string) {
  return Uint8Array.from(atob(text), (c) => c.charCodeAt(0));
}

export function HostFiles({ hostId }: { hostId: string }) {
  const me = useMe();
  const confirm = useConfirm();
  const [roots, setRoots] = useState<Root[]>([]);
  const [root, setRoot] = useState("");
  const [path, setPath] = useState(".");
  const [entries, setEntries] = useState<Entry[]>([]);
  const [selected, setSelected] = useState<{ path: string; info: Entry } | null>(null);
  const [text, setText] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [destination, setDestination] = useState("");
  const [alias, setAlias] = useState("");
  const [viewer, setViewer] = useState(false);
  const [operator, setOperator] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState("");
  const active = useRef(true);
  const pending = useRef<{ input: string; key: string } | null>(null);
  const manager = canManageHost(me.grants, hostId);
  const policy = roots.find((r) => r.alias === root);
  const writer =
    isOwner(me.grants) ||
    me.grants.some(
      (g) => (g.all_hosts || g.host_id === hostId) && policy?.write_roles.includes(g.role),
    );

  useEffect(() => {
    active.current = true;
    void api
      .get<Items<Root>>(`/hosts/${hostId}/file-roots`)
      .then((value) => {
        if (active.current) {
          setRoots(value.items);
          setRoot(value.items[0]?.alias ?? "");
        }
      })
      .catch((e) => {
        if (active.current) setError(errorMessage(e));
      });
    return () => {
      active.current = false;
    };
  }, [hostId]);

  async function execute(q: FileRequest): Promise<Result> {
    const input = JSON.stringify(q);
    if (pending.current?.input !== input) pending.current = { input, key: crypto.randomUUID() };
    const task = await api.post<Task>(`/hosts/${hostId}/files`, q, {
      idempotencyKey: pending.current.key,
    });
    setStatus(`タスク ${task.id} を待っています。`);
    for (let i = 0; i < 95 && active.current; i++) {
      const current = await api.get<Task>(`/tasks/${task.id}`);
      if (current.state === "succeeded" || current.state === "failed") {
        const result = await api.get<Result>(`/tasks/${task.id}/file-result`);
        pending.current = null;
        if (result.error || current.state === "failed")
          throw new Error(
            "ファイル操作に失敗しました。権限・容量・ファイルの変更を確認してください。",
          );
        setStatus("操作が完了しました。");
        return result;
      }
      if (["unknown", "timed_out", "cancelled"].includes(current.state)) {
        throw new Error(
          `タスク ${task.id} は ${current.state} です。ホストの状態を確認してください。`,
        );
      }
      await new Promise((resolve) => setTimeout(resolve, 1000));
    }
    throw new Error(`タスク ${task.id} の結果をまだ確認できません。タスク一覧で確認してください。`);
  }
  async function act(fn: () => Promise<void>) {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (e) {
      if (active.current)
        setError(e instanceof Error && e.name !== "ApiError" ? e.message : errorMessage(e));
    } finally {
      if (active.current) setBusy(false);
    }
  }
  async function list(next = path) {
    const result = await execute({ root, action: "list", path: next });
    setPath(next);
    setEntries(result.entries ?? []);
    setSelected(null);
    setText(null);
  }
  async function mutate(action: string, data?: string) {
    if (!selected) return;
    if (
      !(await confirm({
        title:
          action === "delete"
            ? "削除しますか"
            : action === "move"
              ? "移動しますか"
              : "上書きしますか",
        description:
          selected.path +
          "\n編集・削除前の通常ファイルはホスト側に退避します。復旧にはホスト管理者の操作が必要です。",
        confirmLabel: "実行",
        destructive: action === "delete",
      }))
    )
      return;
    await execute({
      root,
      action,
      path: selected.path,
      etag: selected.info.etag,
      confirm: true,
      ...(action === "move" ? { destination } : {}),
      ...(data !== undefined ? { data } : {}),
    });
    await list();
  }
  const child = (value: string) => (path === "." ? value : `${path}/${value}`);
  const parent = () => (path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : ".");
  const clear = () => {
    setEntries([]);
    setSelected(null);
    setText(null);
  };
  async function select(target: string) {
    const result = await execute({ root, action: "stat", path: target });
    if (result.info) {
      setSelected({ path: target, info: result.info });
      setText(null);
      setDestination(target);
    }
  }
  async function read() {
    if (!selected) return null;
    return execute({ root, action: "read", path: selected.path });
  }
  async function download() {
    const result = await read();
    if (!result || !selected) return;
    const url = URL.createObjectURL(
      new Blob([decode(result.data ?? "").buffer], { type: "application/octet-stream" }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = selected.info.name;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  async function openText() {
    const result = await read();
    if (!result || !selected) return;
    const value = new TextDecoder("utf-8", { fatal: true }).decode(decode(result.data ?? ""));
    if (value.includes("\0")) throw new Error("テキストとして編集できません。");
    setText(value);
    if (result.info) setSelected({ path: selected.path, info: result.info });
  }
  async function save() {
    const data = new TextEncoder().encode(text ?? "");
    if (data.length > limit) throw new Error("256 KiBを超えています。");
    await mutate("write", encode(data));
  }
  async function removeDirectory() {
    const result = await execute({ root, action: "stat", path });
    if (
      result.info &&
      (await confirm({
        title: "空のディレクトリを削除しますか",
        description: path,
        confirmLabel: "削除",
        destructive: true,
      }))
    ) {
      await execute({ root, action: "delete", path, etag: result.info.etag, confirm: true });
      await list(parent());
    }
  }
  async function create(action: "create" | "mkdir", target: string, data?: string) {
    await execute({ root, action, path: child(target), ...(data !== undefined ? { data } : {}) });
    await list();
  }
  async function saveRoot() {
    const updated: Root[] = [
      ...roots.filter((r) => r.alias !== alias),
      {
        alias,
        read_roles: viewer ? ["Admin", "Operator", "Viewer"] : ["Admin", "Operator"],
        write_roles: operator ? ["Admin", "Operator"] : ["Admin"],
      },
    ];
    await api.put(`/hosts/${hostId}/file-roots`, { roots: updated });
    setRoots(updated);
    setRoot(alias);
    clear();
    setPath(".");
    setStatus("ディレクトリの権限を保存しました。");
  }
  async function unpublish(target: Root) {
    if (
      !(await confirm({
        title: "公開を解除しますか",
        description: `${target.alias} のファイル自体は保持されます。`,
        confirmLabel: "解除",
      }))
    )
      return;
    const updated = roots.filter((value) => value.alias !== target.alias);
    await api.put(`/hosts/${hostId}/file-roots`, { roots: updated });
    setRoots(updated);
    if (root === target.alias) {
      setRoot("");
      clear();
    }
  }

  return (
    <section className="flex flex-col gap-4" aria-label="ファイル管理">
      <SectionHeader
        title="ファイル管理"
        description="Agentで公開したディレクトリだけを操作できます。1ファイル256 KiBまで。処理結果はタスクにも記録されます。"
      />
      {error && <ErrorText>{error}</ErrorText>}
      <fieldset disabled={busy} className="flex min-w-0 flex-col gap-4">
        <div className="flex flex-wrap items-end gap-3">
          <div className="w-full sm:w-56">
            <Field id="file-root" label="許可ディレクトリ">
              <NativeSelect
                id="file-root"
                value={root}
                onChange={(e) => {
                  setRoot(e.target.value);
                  setPath(".");
                  clear();
                }}
              >
                <option value="">選択してください</option>
                {roots.map((r) => (
                  <option key={r.alias} value={r.alias}>
                    {r.alias}
                  </option>
                ))}
              </NativeSelect>
            </Field>
          </div>
          {root && (
            <>
              <div className="min-w-48 flex-1">
                <Field id="file-path" label="ディレクトリの相対パス">
                  <Input
                    id="file-path"
                    className="font-mono text-[13px]"
                    value={path}
                    onChange={(e) => {
                      setPath(e.target.value);
                      clear();
                    }}
                  />
                </Field>
              </div>
              <Button onClick={() => void act(() => list())}>一覧を取得</Button>
            </>
          )}
        </div>
        <Notice>{status}</Notice>
        {root && (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>名前</TableHead>
                  <TableHead>種類</TableHead>
                  <TableHead className="text-right">サイズ</TableHead>
                  <TableHead>更新日時</TableHead>
                  <TableHead>
                    <span className="sr-only">操作</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {entries.length === 0 && (
                  <TableEmpty colSpan={5}>表示する項目はありません。</TableEmpty>
                )}
                {entries.map((entry) => {
                  const Icon = entry.directory ? FolderIcon : FileIcon;
                  const current = selected?.path === child(entry.name);
                  return (
                    <TableRow key={entry.name} className={current ? "bg-muted/60" : undefined}>
                      <TableCell>
                        <span className="flex items-center gap-2.5">
                          <Icon aria-hidden className="size-4 shrink-0 text-subtle" />
                          <Button
                            variant="link"
                            size="inline"
                            onClick={() =>
                              void act(() =>
                                entry.directory
                                  ? list(child(entry.name))
                                  : select(child(entry.name)),
                              )
                            }
                          >
                            {entry.name}
                          </Button>
                        </span>
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {entry.directory ? "ディレクトリ" : "ファイル"}
                      </TableCell>
                      <TableCell className="text-right tabular-nums">
                        {formatBytes(entry.size)}
                      </TableCell>
                      <TableCell className="stamp whitespace-nowrap">
                        {formatTime(entry.modified)}
                      </TableCell>
                      <TableCell className="py-1 text-right">
                        {entry.directory && (
                          <Button
                            size="sm"
                            variant="ghost"
                            aria-label={`${entry.name} の情報・整理`}
                            onClick={() => void act(() => select(child(entry.name)))}
                          >
                            情報・整理
                          </Button>
                        )}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
            {path !== "." && (
              <div className="flex flex-wrap gap-2">
                <Button size="sm" variant="outline" onClick={() => void act(() => list(parent()))}>
                  上の階層へ
                </Button>
                {writer && (
                  <Button size="sm" variant="outline" onClick={() => void act(removeDirectory)}>
                    このディレクトリを削除
                  </Button>
                )}
              </div>
            )}
            {selected && (
              <div className="flex flex-col gap-4 rounded-xl border bg-card p-4">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div className="flex min-w-0 flex-col gap-1">
                    <h3 className="font-mono text-sm break-all">{selected.path}</h3>
                    <p className="stamp">
                      {formatBytes(selected.info.size)} ・ {formatTime(selected.info.modified)}
                    </p>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={selected.info.directory}
                      onClick={() => void act(download)}
                    >
                      ダウンロード
                    </Button>
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={selected.info.directory}
                      onClick={() => void act(openText)}
                    >
                      テキストを開く
                    </Button>
                    {writer && (
                      <Button
                        size="sm"
                        variant="outline"
                        className="text-destructive hover:text-destructive"
                        onClick={() => void act(() => mutate("delete"))}
                      >
                        削除
                      </Button>
                    )}
                  </div>
                </div>
                {text !== null && (
                  <>
                    <Field id="file-text" label="UTF-8テキスト">
                      <Textarea
                        id="file-text"
                        spellCheck={false}
                        value={text}
                        readOnly={!writer}
                        onChange={(e) => setText(e.target.value)}
                      />
                    </Field>
                    {writer && (
                      <Button className="w-fit" onClick={() => void act(save)}>
                        変更を保存
                      </Button>
                    )}
                  </>
                )}
                {writer && (
                  <div className="flex flex-wrap items-end gap-2 border-t pt-4">
                    <div className="min-w-48 flex-1">
                      <Field id="file-destination" label="移動先・変更後の相対パス">
                        <Input
                          id="file-destination"
                          className="font-mono text-[13px]"
                          value={destination}
                          onChange={(e) => setDestination(e.target.value)}
                        />
                      </Field>
                    </div>
                    <Button variant="outline" onClick={() => void act(() => mutate("move"))}>
                      名前変更・移動
                    </Button>
                  </div>
                )}
              </div>
            )}
            {writer && (
              <div className="grid gap-4 rounded-xl border bg-card p-4 lg:grid-cols-2">
                <div className="flex flex-wrap items-end gap-2">
                  <div className="min-w-48 flex-1">
                    <Field id="file-new-name" label="新しい名前">
                      <Input
                        id="file-new-name"
                        className="font-mono text-[13px]"
                        value={name}
                        onChange={(e) => setName(e.target.value)}
                      />
                    </Field>
                  </div>
                  <Button variant="outline" onClick={() => void act(() => create("create", name))}>
                    空のファイルを作成
                  </Button>
                  <Button variant="outline" onClick={() => void act(() => create("mkdir", name))}>
                    ディレクトリを作成
                  </Button>
                </div>
                <Field id="file-upload" label="ファイルをアップロード（同名があれば拒否）">
                  <Input
                    id="file-upload"
                    type="file"
                    className="py-1.5 text-muted-foreground file:mr-3 file:text-sm file:text-foreground"
                    onChange={(e) => {
                      const file = e.target.files?.[0];
                      e.target.value = "";
                      if (file)
                        void act(async () => {
                          if (file.size > limit) throw new Error("256 KiBを超えています。");
                          await create(
                            "create",
                            file.name,
                            encode(new Uint8Array(await file.arrayBuffer())),
                          );
                        });
                    }}
                  />
                </Field>
              </div>
            )}
          </>
        )}
        {manager && (
          <details className="group rounded-xl border">
            <summary className="flex h-11 cursor-pointer items-center px-4 text-sm outline-none select-none focus-visible:ring-[3px] focus-visible:ring-ring/50">
              公開ディレクトリの権限設定
            </summary>
            <div className="grid max-w-2xl gap-4 border-t p-4">
              <p className="text-sm text-muted-foreground">
                Agentの設定にあるルート名を登録します。物理パスの指定はホスト側で行ってください。同じ名前を登録すると権限を更新します。
              </p>
              <Field id="file-alias" label="ルート名">
                <Input id="file-alias" value={alias} onChange={(e) => setAlias(e.target.value)} />
              </Field>
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={viewer}
                  onChange={(e) => setViewer(e.target.checked)}
                />
                Viewerに閲覧を許可
              </label>
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={operator}
                  onChange={(e) => setOperator(e.target.checked)}
                />
                Operatorに変更を許可（閲覧は常に許可）
              </label>
              <Button className="w-fit" onClick={() => void act(saveRoot)}>
                ルートの権限を保存
              </Button>
              {roots.length > 0 && (
                <ul className="divide-y border-t">
                  {roots.map((r) => (
                    <li key={r.alias} className="flex items-center justify-between gap-3 py-2">
                      <span className="flex min-w-0 flex-col gap-0.5 text-sm">
                        <span className="font-mono text-[13px]">{r.alias}</span>
                        <span className="text-xs text-muted-foreground">
                          閲覧: {r.read_roles.join(", ")} / 変更: {r.write_roles.join(", ")}
                        </span>
                      </span>
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => void act(() => unpublish(r))}
                      >
                        公開解除
                      </Button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </details>
        )}
      </fieldset>
    </section>
  );
}
