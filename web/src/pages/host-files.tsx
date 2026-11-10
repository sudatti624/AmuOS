import { useEffect, useRef, useState } from "react";
import { useMe } from "@/app/session";
import { useConfirm } from "@/app/confirm";
import { ErrorText, Field } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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

  return (
    <section className="grid gap-4 rounded-lg border p-4" aria-label="ファイル管理">
      <h2 className="font-semibold">ファイル管理</h2>
      <p className="text-sm text-muted-foreground">
        Agentで公開したディレクトリだけを操作できます。1ファイル256
        KiBまで。処理結果はタスクにも記録されます。
      </p>
      {error && <ErrorText>{error}</ErrorText>}
      <p role="status" className="break-all text-sm">
        {status}
      </p>
      <fieldset disabled={busy} className="grid gap-4">
        <Field id="file-root" label="許可ディレクトリ">
          <select
            id="file-root"
            className="rounded-md border bg-background p-2"
            value={root}
            onChange={(e) => {
              setRoot(e.target.value);
              setPath(".");
              setEntries([]);
              setSelected(null);
              setText(null);
            }}
          >
            <option value="">選択してください</option>
            {roots.map((r) => (
              <option key={r.alias} value={r.alias}>
                {r.alias}
              </option>
            ))}
          </select>
        </Field>
        {root && (
          <>
            <div className="flex flex-wrap gap-2">
              <div className="min-w-48 flex-1">
                <Field id="file-path" label="ディレクトリの相対パス">
                  <Input
                    id="file-path"
                    value={path}
                    onChange={(e) => {
                      setPath(e.target.value);
                      setSelected(null);
                      setEntries([]);
                      setText(null);
                    }}
                  />
                </Field>
              </div>
              <Button className="self-end" onClick={() => void act(() => list())}>
                一覧を取得
              </Button>
            </div>
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr>
                    <th>名前</th>
                    <th>種類</th>
                    <th>サイズ</th>
                    <th>更新日時</th>
                  </tr>
                </thead>
                <tbody>
                  {entries.map((entry) => (
                    <tr key={entry.name} className="border-t">
                      <td>
                        <Button
                          variant="link"
                          onClick={() =>
                            void act(async () => {
                              const target = child(entry.name);
                              if (entry.directory) {
                                await list(target);
                                return;
                              }
                              const result = await execute({ root, action: "stat", path: target });
                              if (result.info) {
                                setSelected({ path: target, info: result.info });
                                setText(null);
                                setDestination(target);
                              }
                            })
                          }
                        >
                          {entry.name}
                        </Button>
                        {entry.directory && (
                          <Button
                            size="sm"
                            variant="outline"
                            aria-label={`${entry.name} の情報・整理`}
                            onClick={() =>
                              void act(async () => {
                                const target = child(entry.name);
                                const result = await execute({
                                  root,
                                  action: "stat",
                                  path: target,
                                });
                                if (result.info) {
                                  setSelected({ path: target, info: result.info });
                                  setText(null);
                                  setDestination(target);
                                }
                              })
                            }
                          >
                            情報・整理
                          </Button>
                        )}
                      </td>
                      <td>{entry.directory ? "ディレクトリ" : "ファイル"}</td>
                      <td>{formatBytes(entry.size)}</td>
                      <td>{formatTime(entry.modified)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="flex flex-wrap gap-2">
              {path !== "." && (
                <Button
                  variant="outline"
                  onClick={() =>
                    void act(() =>
                      list(path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : "."),
                    )
                  }
                >
                  上の階層へ
                </Button>
              )}
              {path !== "." && writer && (
                <Button
                  variant="outline"
                  onClick={() =>
                    void act(async () => {
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
                        await execute({
                          root,
                          action: "delete",
                          path,
                          etag: result.info.etag,
                          confirm: true,
                        });
                        await list(path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : ".");
                      }
                    })
                  }
                >
                  このディレクトリを削除
                </Button>
              )}
            </div>
            {selected && (
              <div className="grid gap-3 rounded-md border p-3">
                <h3 className="break-all font-medium">{selected.path}</h3>
                <p className="text-sm">
                  {formatBytes(selected.info.size)} ・ {formatTime(selected.info.modified)}
                </p>
                <div className="flex flex-wrap gap-2">
                  <Button
                    variant="outline"
                    disabled={selected.info.directory}
                    onClick={() =>
                      void act(async () => {
                        const result = await execute({ root, action: "read", path: selected.path });
                        const url = URL.createObjectURL(
                          new Blob([decode(result.data ?? "").buffer], {
                            type: "application/octet-stream",
                          }),
                        );
                        const a = document.createElement("a");
                        a.href = url;
                        a.download = selected.info.name;
                        a.click();
                        setTimeout(() => URL.revokeObjectURL(url), 1000);
                      })
                    }
                  >
                    ダウンロード
                  </Button>
                  <Button
                    variant="outline"
                    disabled={selected.info.directory}
                    onClick={() =>
                      void act(async () => {
                        const result = await execute({ root, action: "read", path: selected.path });
                        const value = new TextDecoder("utf-8", { fatal: true }).decode(
                          decode(result.data ?? ""),
                        );
                        if (value.includes("\0")) throw new Error("テキストとして編集できません。");
                        setText(value);
                        if (result.info) setSelected({ path: selected.path, info: result.info });
                      })
                    }
                  >
                    テキストを開く
                  </Button>
                  {writer && (
                    <Button variant="destructive" onClick={() => void act(() => mutate("delete"))}>
                      削除
                    </Button>
                  )}
                </div>
                {text !== null && (
                  <>
                    <Field id="file-text" label="UTF-8テキスト">
                      <textarea
                        id="file-text"
                        className="min-h-48 rounded-md border p-2 font-mono text-sm"
                        value={text}
                        readOnly={!writer}
                        onChange={(e) => setText(e.target.value)}
                      />
                    </Field>
                    {writer && (
                      <Button
                        onClick={() =>
                          void act(async () => {
                            const data = new TextEncoder().encode(text);
                            if (data.length > limit) throw new Error("256 KiBを超えています。");
                            await mutate("write", encode(data));
                          })
                        }
                      >
                        変更を保存
                      </Button>
                    )}
                  </>
                )}
                {writer && (
                  <div className="flex flex-wrap gap-2">
                    <div className="min-w-48 flex-1">
                      <Field id="file-destination" label="移動先・変更後の相対パス">
                        <Input
                          id="file-destination"
                          value={destination}
                          onChange={(e) => setDestination(e.target.value)}
                        />
                      </Field>
                    </div>
                    <Button className="self-end" onClick={() => void act(() => mutate("move"))}>
                      名前変更・移動
                    </Button>
                  </div>
                )}
              </div>
            )}
            {writer && (
              <div className="grid gap-3 rounded-md border p-3">
                <Field id="file-new-name" label="新しい名前">
                  <Input
                    id="file-new-name"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                  />
                </Field>
                <div className="flex flex-wrap gap-2">
                  <Button
                    variant="outline"
                    onClick={() =>
                      void act(async () => {
                        await execute({ root, action: "create", path: child(name) });
                        await list();
                      })
                    }
                  >
                    空のファイルを作成
                  </Button>
                  <Button
                    variant="outline"
                    onClick={() =>
                      void act(async () => {
                        await execute({ root, action: "mkdir", path: child(name) });
                        await list();
                      })
                    }
                  >
                    ディレクトリを作成
                  </Button>
                </div>
                <Field id="file-upload" label="ファイルをアップロード（同名があれば拒否）">
                  <Input
                    id="file-upload"
                    type="file"
                    onChange={(e) => {
                      const file = e.target.files?.[0];
                      e.target.value = "";
                      if (file)
                        void act(async () => {
                          if (file.size > limit) throw new Error("256 KiBを超えています。");
                          await execute({
                            root,
                            action: "create",
                            path: child(file.name),
                            data: encode(new Uint8Array(await file.arrayBuffer())),
                          });
                          await list();
                        });
                    }}
                  />
                </Field>
              </div>
            )}
          </>
        )}
        {manager && (
          <details className="rounded-md border p-3">
            <summary className="cursor-pointer font-medium">公開ディレクトリの権限設定</summary>
            <div className="mt-3 grid gap-3">
              <p className="text-sm text-muted-foreground">
                Agentの設定にあるルート名を登録します。物理パスの指定はホスト側で行ってください。同じ名前を登録すると権限を更新します。
              </p>
              <Field id="file-alias" label="ルート名">
                <Input id="file-alias" value={alias} onChange={(e) => setAlias(e.target.value)} />
              </Field>
              <label className="flex gap-2">
                <input
                  type="checkbox"
                  checked={viewer}
                  onChange={(e) => setViewer(e.target.checked)}
                />
                Viewerに閲覧を許可
              </label>
              <label className="flex gap-2">
                <input
                  type="checkbox"
                  checked={operator}
                  onChange={(e) => setOperator(e.target.checked)}
                />
                Operatorに変更を許可（閲覧は常に許可）
              </label>
              <Button
                onClick={() =>
                  void act(async () => {
                    const updated: Root[] = [
                      ...roots.filter((r) => r.alias !== alias),
                      {
                        alias,
                        read_roles: viewer
                          ? ["Admin", "Operator", "Viewer"]
                          : ["Admin", "Operator"],
                        write_roles: operator ? ["Admin", "Operator"] : ["Admin"],
                      },
                    ];
                    await api.put(`/hosts/${hostId}/file-roots`, { roots: updated });
                    setRoots(updated);
                    setRoot(alias);
                    setEntries([]);
                    setSelected(null);
                    setText(null);
                    setPath(".");
                    setStatus("ディレクトリの権限を保存しました。");
                  })
                }
              >
                ルートの権限を保存
              </Button>
              {roots.map((r) => (
                <div key={r.alias} className="flex items-center justify-between gap-2">
                  <span>
                    {r.alias}（閲覧: {r.read_roles.join(", ")} / 変更: {r.write_roles.join(", ")}）
                  </span>
                  <Button
                    variant="outline"
                    onClick={() =>
                      void act(async () => {
                        if (
                          !(await confirm({
                            title: "公開を解除しますか",
                            description: `${r.alias} のファイル自体は保持されます。`,
                            confirmLabel: "解除",
                          }))
                        )
                          return;
                        const updated = roots.filter((value) => value.alias !== r.alias);
                        await api.put(`/hosts/${hostId}/file-roots`, { roots: updated });
                        setRoots(updated);
                        if (root === r.alias) {
                          setRoot("");
                          setEntries([]);
                          setSelected(null);
                          setText(null);
                        }
                      })
                    }
                  >
                    公開解除
                  </Button>
                </div>
              ))}
            </div>
          </details>
        )}
      </fieldset>
    </section>
  );
}
