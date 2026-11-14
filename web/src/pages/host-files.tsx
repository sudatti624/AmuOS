import { Breadcrumbs, FileListing, type FileEntry as Entry } from "./file-browser-view";
import { useEffect, useEffectEvent, useRef, useState } from "react";
import { useMe } from "@/app/session";
import { useConfirm } from "@/app/confirm";
import {
  ArrowUpIcon,
  ChevronRightIcon,
  DownloadIcon,
  FileIcon,
  FilePlusIcon,
  FolderIcon,
  FolderInputIcon,
  FolderPlusIcon,
  HardDriveIcon,
  LayoutGridIcon,
  ListIcon,
  LoaderCircleIcon,
  PencilIcon,
  RefreshCwIcon,
  SearchIcon,
  Trash2Icon,
  UploadIcon,
  XIcon,
} from "lucide-react";
import { ErrorText, Field, Notice, SectionHeader } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Input, NativeSelect, Textarea } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api, errorMessage, type Items, type Role, type Task } from "@/lib/api";
import { canManageHost, isOwner } from "@/lib/permissions";
import { formatBytes, formatTime } from "@/lib/format";

interface Root {
  alias: string;
  read_roles: Role[];
  write_roles: Role[];
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
type NameAction = "create" | "mkdir" | "rename";
const limit = 256 * 1024;
const join = (path: string, name: string) => (path === "." ? name : `${path}/${name}`);
const parent = (path: string) => (path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : ".");
function encode(bytes: Uint8Array) {
  let text = "";
  for (const b of bytes) text += String.fromCharCode(b);
  return btoa(text);
}
function decode(text: string) {
  return Uint8Array.from(atob(text), (c) => c.charCodeAt(0));
}

export function HostFiles({ hostId, visible = true }: { hostId: string; visible?: boolean }) {
  const me = useMe();
  const confirm = useConfirm();
  const [roots, setRoots] = useState<Root[]>([]);
  const [root, setRoot] = useState("");
  const [path, setPath] = useState(".");
  const [entries, setEntries] = useState<Entry[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [selected, setSelected] = useState<{ path: string; info: Entry } | null>(null);
  const [text, setText] = useState<string | null>(null);
  const [originalText, setOriginalText] = useState("");
  const [name, setName] = useState("");
  const [nameAction, setNameAction] = useState<NameAction | null>(null);
  const [moving, setMoving] = useState(false);
  const [movePath, setMovePath] = useState(".");
  const [moveEntries, setMoveEntries] = useState<Entry[]>([]);
  const [query, setQuery] = useState("");
  const [view, setView] = useState<"list" | "grid">("list");
  const [sort, setSort] = useState("name");
  const [dragging, setDragging] = useState(false);
  const [alias, setAlias] = useState("");
  const [viewer, setViewer] = useState(false);
  const [operator, setOperator] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState("");
  const active = useRef(true);
  const locked = useRef(false);
  const autoRoot = useRef("");
  const uploadInput = useRef<HTMLInputElement>(null);
  const editor = useRef<HTMLElement>(null);
  const pending = useRef<{ input: string; key: string } | null>(null);
  const manager = canManageHost(me.grants, hostId);
  const policy = roots.find((r) => r.alias === root);
  const writer =
    isOwner(me.grants) ||
    me.grants.some(
      (g) => (g.all_hosts || g.host_id === hostId) && policy?.write_roles.includes(g.role),
    );
  const dirty = text !== null && text !== originalText;
  const editing = text !== null;

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
  useEffect(() => {
    if (!dirty) return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);
  useEffect(() => {
    if (editing) editor.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [editing]);

  async function execute(q: FileRequest, progress = "ホストで処理しています…"): Promise<Result> {
    if (!active.current) throw new Error("画面を閉じたため待機を終了しました。");
    const input = JSON.stringify(q);
    if (pending.current?.input !== input) pending.current = { input, key: crypto.randomUUID() };
    const task = await api.post<Task>(`/hosts/${hostId}/files`, q, {
      idempotencyKey: pending.current.key,
    });
    setStatus(progress);
    for (let i = 0; i < 95 && active.current; i++) {
      const current = await api.get<Task>(`/tasks/${task.id}`);
      if (current.state === "succeeded" || current.state === "failed") {
        const result = await api.get<Result>(`/tasks/${task.id}/file-result`);
        pending.current = null;
        if (result.error || current.state === "failed")
          throw new Error(
            "ファイル操作に失敗しました。権限・容量・ファイルの変更を確認してください。",
          );
        setStatus("");
        return result;
      }
      if (["unknown", "timed_out", "cancelled"].includes(current.state))
        throw new Error(
          `タスク ${task.id} は ${current.state} です。ホストの状態を確認してください。`,
        );
      await new Promise((resolve) => setTimeout(resolve, 1000));
    }
    throw new Error(`タスク ${task.id} の結果をまだ確認できません。タスク一覧で確認してください。`);
  }
  async function act(fn: () => Promise<void>) {
    if (locked.current) return;
    locked.current = true;
    setBusy(true);
    setError(null);
    setStatus("");
    try {
      await fn();
    } catch (e) {
      if (active.current) {
        setStatus("");
        setError(e instanceof Error && e.name !== "ApiError" ? e.message : errorMessage(e));
      }
    } finally {
      locked.current = false;
      if (active.current) setBusy(false);
    }
  }
  const clearSelection = () => {
    setSelected(null);
    setText(null);
    setOriginalText("");
  };
  async function discard() {
    return (
      !dirty ||
      confirm({
        title: "編集内容を破棄しますか",
        description: "保存していない変更があります。",
        confirmLabel: "破棄する",
        destructive: true,
      })
    );
  }
  async function list(next = path, nextRoot = root) {
    const result = await execute({ root: nextRoot, action: "list", path: next });
    if (!active.current) return;
    autoRoot.current = nextRoot;
    setRoot(nextRoot);
    setPath(next);
    setEntries(result.entries ?? []);
    setLoaded(true);
    setQuery("");
    clearSelection();
  }
  async function navigate(next: string, nextRoot = root) {
    if (await discard()) await list(next, nextRoot);
  }
  const autoLoad = useEffectEvent(() => {
    autoRoot.current = root;
    void act(() => list(".", root));
  });
  useEffect(() => {
    if (visible && root && !busy && autoRoot.current !== root) autoLoad();
  }, [visible, root, busy]);
  async function choose(entry: Entry) {
    if (selected?.path === join(path, entry.name) || !(await discard())) return;
    setSelected({ path: join(path, entry.name), info: entry });
    setText(null);
  }
  async function open(entry: Entry) {
    if (!(await discard())) return;
    if (entry.directory) {
      await list(join(path, entry.name));
      return;
    }
    const target = join(path, entry.name);
    setSelected({ path: target, info: entry });
    setText(null);
    const result = await execute({ root, action: "read", path: target });
    let value: string;
    try {
      value = new TextDecoder("utf-8", { fatal: true }).decode(decode(result.data ?? ""));
    } catch {
      throw new Error("UTF-8テキストとして開けません。ダウンロードして確認してください。");
    }
    if (value.includes("\0"))
      throw new Error("テキストとして開けません。ダウンロードして確認してください。");
    setText(value);
    setOriginalText(value);
    if (result.info) setSelected({ path: target, info: result.info });
  }
  async function download() {
    if (!selected || selected.info.directory) return;
    const result = await execute({ root, action: "read", path: selected.path });
    const url = URL.createObjectURL(
      new Blob([decode(result.data ?? "").buffer], { type: "application/octet-stream" }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = selected.info.name;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  async function remove() {
    if (!selected) return;
    if (
      !(await confirm({
        title: "削除しますか",
        description: `${selected.info.name}\n${selected.info.directory ? "空のフォルダだけを削除できます。" : "削除前のファイルはホスト側に退避します。復旧にはホスト管理者の操作が必要です。"}`,
        confirmLabel: "削除",
        destructive: true,
      }))
    )
      return;
    await execute({
      root,
      action: "delete",
      path: selected.path,
      etag: selected.info.etag,
      confirm: true,
    });
    await list();
    setStatus("削除しました。");
  }
  async function save() {
    if (!selected || text === null) return;
    const data = new TextEncoder().encode(text);
    if (data.length > limit) throw new Error("256 KiBを超えています。");
    if (
      !(await confirm({
        title: "変更を保存しますか",
        description: `${selected.info.name} を上書きします。編集前のファイルはホスト側に退避します。`,
        confirmLabel: "保存",
      }))
    )
      return;
    await execute({
      root,
      action: "write",
      path: selected.path,
      etag: selected.info.etag,
      confirm: true,
      data: encode(data),
    });
    const result = await execute({ root, action: "read", path: selected.path });
    if (result.info) {
      setSelected({ path: selected.path, info: result.info });
      setEntries((items) =>
        items.map((entry) => (entry.name === selected.info.name ? result.info! : entry)),
      );
    }
    setOriginalText(text);
    setStatus("変更を保存しました。");
  }
  async function upload(files: File[]) {
    if (!files.length || !(await discard())) return;
    if (files.some((file) => file.size > limit))
      throw new Error(
        "256 KiBを超えるファイルがあります。1ファイル256 KiBまでアップロードできます。",
      );
    clearSelection();
    let completed = 0;
    try {
      for (const file of files) {
        await execute(
          {
            root,
            action: "create",
            path: join(path, file.name),
            data: encode(new Uint8Array(await file.arrayBuffer())),
          },
          `アップロード中 ${completed + 1} / ${files.length}：${file.name}`,
        );
        completed++;
      }
    } catch (e) {
      try {
        await list();
      } catch {
        /* Keep the original failure. */
      }
      throw new Error(
        `${completed} / ${files.length} 件をアップロードしました。${e instanceof Error && e.name !== "ApiError" ? e.message : errorMessage(e)} 残りのファイルは送信していません。`,
      );
    }
    await list();
    setStatus(`${completed}件のファイルをアップロードしました。`);
  }
  async function beginName(action: NameAction) {
    if (!(await discard())) return;
    setText(null);
    setNameAction(action);
    setName(action === "rename" ? (selected?.info.name ?? "") : "");
  }
  async function submitName() {
    if (!name || name === "." || name === ".." || /[\\/]/.test(name))
      throw new Error("名前にはパスを指定できません。");
    if (nameAction === "rename") {
      if (!selected) return;
      await execute({
        root,
        action: "move",
        path: selected.path,
        destination: join(path, name),
        etag: selected.info.etag,
        confirm: true,
      });
    } else if (nameAction)
      await execute({
        root,
        action: nameAction,
        path: join(path, name),
        ...(nameAction === "create" ? { data: "" } : {}),
      });
    setNameAction(null);
    await list();
    setStatus("保存しました。");
  }
  async function browseMove(next: string) {
    const result = await execute({ root, action: "list", path: next });
    setMovePath(next);
    setMoveEntries((result.entries ?? []).filter((e) => e.directory));
  }
  async function beginMove() {
    if (!(await discard())) return;
    setText(null);
    setMoving(true);
    setMovePath(path);
    setMoveEntries([]);
    await browseMove(path);
  }
  async function move() {
    if (!selected) return;
    await execute({
      root,
      action: "move",
      path: selected.path,
      destination: join(movePath, selected.info.name),
      etag: selected.info.etag,
      confirm: true,
    });
    setMoving(false);
    await list();
    setStatus("移動しました。");
  }
  async function saveRoot() {
    if (!(await discard())) return;
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
    await list(".", alias);
    setStatus("ディレクトリの権限を保存しました。");
  }
  async function unpublish(target: Root) {
    if (
      !(await discard()) ||
      !(await confirm({
        title: "公開を解除しますか",
        description: `${target.alias} のファイル自体は保持されます。`,
        confirmLabel: "解除",
      }))
    )
      return;
    const updated = roots.filter((r) => r.alias !== target.alias);
    await api.put(`/hosts/${hostId}/file-roots`, { roots: updated });
    setRoots(updated);
    if (root === target.alias) {
      setRoot(updated[0]?.alias ?? "");
      setEntries([]);
      setLoaded(false);
      clearSelection();
      autoRoot.current = "";
    }
  }
  const shown = entries
    .filter((entry) => entry.name.toLocaleLowerCase().includes(query.toLocaleLowerCase()))
    .sort(
      (a, b) =>
        Number(b.directory) - Number(a.directory) ||
        (sort === "modified" ? b.modified - a.modified : sort === "size" ? b.size - a.size : 0) ||
        a.name.localeCompare(b.name, "ja", { numeric: true }),
    );
  const nameTitle =
    nameAction === "mkdir"
      ? "新しいフォルダ"
      : nameAction === "rename"
        ? "名前を変更"
        : "新しいファイル";
  const moveInvalid =
    !selected ||
    movePath === path ||
    (selected.info.directory &&
      (movePath === selected.path || movePath.startsWith(`${selected.path}/`)));

  return (
    <section className="flex min-w-0 flex-col gap-4" aria-label="ファイル管理">
      <SectionHeader
        title="ファイル管理"
        description="ファイルを開いて編集、フォルダを開いて移動。1ファイル256 KiBまで。"
      />
      {error && <ErrorText>{error}</ErrorText>}
      <div className="overflow-hidden rounded-xl border bg-card lg:grid lg:grid-cols-[180px_minmax(0,1fr)]">
        <aside
          aria-label="許可ディレクトリ"
          className="border-b bg-muted/20 p-3 lg:border-r lg:border-b-0"
        >
          <p className="mb-3 px-2 text-xs font-medium text-subtle">許可ディレクトリ</p>
          <div className="flex flex-wrap gap-1 lg:flex-col">
            {roots.map((r) => (
              <Button
                key={r.alias}
                variant="ghost"
                disabled={busy}
                className={`h-auto min-h-9 min-w-0 justify-start px-2 py-2 text-left whitespace-normal break-all ${root === r.alias ? "bg-accent" : "text-muted-foreground"}`}
                aria-pressed={root === r.alias}
                onClick={() => void act(() => navigate(".", r.alias))}
              >
                <HardDriveIcon />
                {r.alias}
              </Button>
            ))}
            {!roots.length && (
              <p className="px-2 text-xs leading-5 text-muted-foreground">
                公開されているディレクトリはありません。
              </p>
            )}
          </div>
          {root && (
            <p className="mt-5 hidden px-2 text-xs text-subtle lg:block">
              {writer ? "閲覧・変更可能" : "閲覧のみ"}
            </p>
          )}
        </aside>
        <div
          className="relative min-w-0"
          onDragOver={(e) => {
            if (root && writer && !busy && e.dataTransfer.types.includes("Files")) {
              e.preventDefault();
              e.dataTransfer.dropEffect = "copy";
              setDragging(true);
            }
          }}
          onDragLeave={(e) => {
            if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false);
          }}
          onDrop={(e) => {
            e.preventDefault();
            setDragging(false);
            if (!root || !writer || locked.current) return;
            const files = Array.from(e.dataTransfer.files);
            const folder = Array.from(e.dataTransfer.items).some(
              (item) => item.webkitGetAsEntry?.()?.isDirectory,
            );
            void act(async () => {
              if (folder)
                throw new Error(
                  "フォルダのアップロードには対応していません。ファイルを選んでください。",
                );
              await upload(files);
            });
          }}
        >
          {dragging && (
            <div className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center border-2 border-dashed border-foreground bg-background/95 p-6 text-center">
              <div className="flex flex-col items-center gap-3">
                <UploadIcon className="size-8" />
                <span className="font-medium">ここにドロップしてアップロード</span>
                <span className="text-sm text-muted-foreground">
                  現在のフォルダへ追加・1ファイル256 KiBまで
                </span>
              </div>
            </div>
          )}
          {root ? (
            <>
              <fieldset disabled={busy} className="flex min-w-0 flex-col">
                <div className="flex flex-wrap items-center gap-2 border-b px-3 py-3">
                  <Button
                    size="icon"
                    variant="ghost"
                    aria-label="上の階層へ"
                    title="上の階層へ"
                    disabled={path === "."}
                    onClick={() => void act(() => navigate(parent(path)))}
                  >
                    <ArrowUpIcon />
                  </Button>
                  <div className="min-w-0 flex-1">
                    <Breadcrumbs
                      root={root}
                      path={path}
                      disabled={busy}
                      onNavigate={(next) => void act(() => navigate(next))}
                    />
                  </div>
                  <Button
                    size="icon"
                    variant="ghost"
                    aria-label="一覧を更新"
                    title="一覧を更新"
                    onClick={() => void act(() => navigate(path))}
                  >
                    <RefreshCwIcon />
                  </Button>
                </div>
                <div className="flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3">
                  <div className="flex flex-wrap gap-2">
                    {writer && (
                      <>
                        <Button
                          size="sm"
                          disabled={!loaded}
                          onClick={() => uploadInput.current?.click()}
                        >
                          <UploadIcon />
                          アップロード
                        </Button>
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={!loaded}
                          onClick={() => void act(() => beginName("mkdir"))}
                        >
                          <FolderPlusIcon />
                          新しいフォルダ
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={!loaded}
                          onClick={() => void act(() => beginName("create"))}
                        >
                          <FilePlusIcon />
                          新しいファイル
                        </Button>
                      </>
                    )}
                  </div>
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    <div className="relative w-44 max-w-full">
                      <SearchIcon
                        aria-hidden
                        className="pointer-events-none absolute top-2.5 left-2.5 size-3.5 text-subtle"
                      />
                      <Input
                        aria-label="このフォルダ内を検索"
                        placeholder="名前で検索"
                        className="h-8 pl-8"
                        value={query}
                        onChange={(e) => setQuery(e.target.value)}
                      />
                    </div>
                    <NativeSelect
                      aria-label="並び順"
                      className="h-8 w-24 text-xs"
                      value={sort}
                      onChange={(e) => setSort(e.target.value)}
                    >
                      <option value="name">名前順</option>
                      <option value="modified">更新日時</option>
                      <option value="size">サイズ順</option>
                    </NativeSelect>
                    <div className="flex rounded-md border p-0.5">
                      <Button
                        variant="ghost"
                        size="sm"
                        className={`size-7 p-0 ${view === "list" ? "bg-accent" : ""}`}
                        aria-label="リスト表示"
                        aria-pressed={view === "list"}
                        onClick={() => setView("list")}
                      >
                        <ListIcon />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        className={`size-7 p-0 ${view === "grid" ? "bg-accent" : ""}`}
                        aria-label="グリッド表示"
                        aria-pressed={view === "grid"}
                        onClick={() => setView("grid")}
                      >
                        <LayoutGridIcon />
                      </Button>
                    </div>
                  </div>
                </div>
                <input
                  ref={uploadInput}
                  type="file"
                  multiple
                  className="sr-only"
                  aria-label="ファイルをアップロード"
                  disabled={!writer || busy || !loaded}
                  onChange={(e) => {
                    const files = Array.from(e.target.files ?? []);
                    e.target.value = "";
                    void act(() => upload(files));
                  }}
                />
                <div
                  className="flex min-h-12 flex-wrap items-center gap-1 border-b bg-muted/15 px-4 py-2"
                  aria-label="選択した項目の操作"
                >
                  <span className="mr-auto min-w-0 py-1 text-xs text-muted-foreground">
                    {selected ? (
                      <span className="font-medium break-all text-foreground">
                        {selected.info.name}
                      </span>
                    ) : (
                      "項目を選択すると操作できます"
                    )}
                  </span>
                  {selected && (
                    <>
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={!selected}
                        onClick={() => selected && void act(() => open(selected.info))}
                      >
                        <FileIcon />
                        開く
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={!selected || selected.info.directory}
                        onClick={() => void act(download)}
                      >
                        <DownloadIcon />
                        <span className="hidden sm:inline">ダウンロード</span>
                        <span className="sr-only sm:hidden">ダウンロード</span>
                      </Button>
                      {writer && (
                        <>
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={!selected}
                            onClick={() => void act(() => beginName("rename"))}
                          >
                            <PencilIcon />
                            <span className="hidden sm:inline">名前変更</span>
                            <span className="sr-only sm:hidden">名前変更</span>
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={!selected}
                            onClick={() => void act(beginMove)}
                          >
                            <FolderInputIcon />
                            <span className="hidden sm:inline">移動</span>
                            <span className="sr-only sm:hidden">移動</span>
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            className="text-destructive hover:text-destructive"
                            disabled={!selected}
                            onClick={() => void act(remove)}
                          >
                            <Trash2Icon />
                            <span className="hidden sm:inline">削除</span>
                            <span className="sr-only sm:hidden">削除</span>
                          </Button>
                        </>
                      )}
                      {selected && (
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label="選択を解除"
                          onClick={() =>
                            void act(async () => {
                              if (await discard()) clearSelection();
                            })
                          }
                        >
                          <XIcon />
                        </Button>
                      )}
                    </>
                  )}
                </div>
                <div className="min-h-64 p-2" aria-busy={busy}>
                  {!loaded ? (
                    <div className="flex min-h-60 flex-col items-center justify-center gap-3 text-sm text-muted-foreground">
                      <FolderIcon className="size-9 text-subtle" />
                      <p>
                        {busy
                          ? "ファイルを読み込んでいます…"
                          : "一覧を更新してファイルを表示してください。"}
                      </p>
                    </div>
                  ) : !shown.length ? (
                    <div className="flex min-h-60 flex-col items-center justify-center gap-2 text-sm text-muted-foreground">
                      <FolderIcon className="mb-2 size-9 text-subtle" />
                      <p>{query ? "一致する項目はありません" : "このフォルダは空です"}</p>
                      {!query && writer && (
                        <p className="text-xs">
                          ファイルをドロップ、または「アップロード」から追加
                        </p>
                      )}
                    </div>
                  ) : (
                    <FileListing
                      entries={shown}
                      selectedPath={selected?.path}
                      path={path}
                      view={view}
                      onSelect={(entry) => void act(() => choose(entry))}
                      onOpen={(entry) => void act(() => open(entry))}
                      onToggle={(entry) =>
                        void act(async () => {
                          if (selected?.path === join(path, entry.name)) {
                            if (await discard()) clearSelection();
                          } else await choose(entry);
                        })
                      }
                    />
                  )}
                </div>
              </fieldset>
              <div className="flex min-h-10 flex-wrap items-center gap-3 border-t px-4 py-2 text-xs text-muted-foreground">
                <span>{query ? `${shown.length} / ${entries.length}` : entries.length}項目</span>
                {busy && <LoaderCircleIcon aria-label="処理中" className="size-3.5 animate-spin" />}
                <Notice>{status}</Notice>
                <span className="ml-auto">{writer ? "ドロップしてアップロード" : "閲覧のみ"}</span>
              </div>
            </>
          ) : (
            <div className="flex min-h-80 flex-col items-center justify-center gap-3 p-6 text-center text-sm text-muted-foreground">
              <FolderIcon className="size-10 text-subtle" />
              <p>利用できるディレクトリがありません</p>
              <p className="max-w-sm text-xs leading-5">
                {manager
                  ? "下の「公開ディレクトリの権限設定」で、Agentに設定したルートを登録してください。"
                  : "ホスト管理者にディレクトリの公開と権限設定を依頼してください。"}
              </p>
            </div>
          )}
        </div>
      </div>
      {text !== null && selected && (
        <section
          ref={editor}
          className="overflow-hidden rounded-xl border bg-card"
          aria-label="テキストエディタ"
        >
          <div className="flex flex-wrap items-center gap-3 border-b px-4 py-3">
            <FileIcon className="size-4 text-subtle" />
            <h3 className="min-w-0 flex-1 text-sm font-medium break-all">
              {selected.info.name}
              <span className="ml-2 text-xs font-normal text-subtle">
                {dirty ? "未保存" : "保存済み"}
                {!writer && "・閲覧のみ"}
              </span>
            </h3>
            {writer && (
              <Button size="sm" disabled={busy || !dirty} onClick={() => void act(save)}>
                変更を保存
              </Button>
            )}
            <Button
              size="icon"
              variant="ghost"
              aria-label="エディタを閉じる"
              disabled={busy}
              onClick={() =>
                void act(async () => {
                  if (await discard()) setText(null);
                })
              }
            >
              <XIcon />
            </Button>
          </div>
          <div className="p-4">
            <Field id="file-text" label="UTF-8テキスト">
              <Textarea
                id="file-text"
                className="min-h-80 font-mono text-[13px] leading-6"
                spellCheck={false}
                value={text}
                readOnly={!writer || busy}
                onChange={(e) => setText(e.target.value)}
              />
            </Field>
          </div>
          <p className="border-t px-4 py-2 text-xs text-muted-foreground">
            {formatBytes(new TextEncoder().encode(text).length)} / 256 KiB ・{" "}
            {formatTime(selected.info.modified)}
          </p>
        </section>
      )}
      <Dialog
        open={nameAction !== null}
        onOpenChange={(open) => {
          if (!open && !busy) setNameAction(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{nameTitle}</DialogTitle>
            <DialogDescription>
              {root} / {path === "." ? "" : path} に保存します。同じ名前の項目は上書きしません。
            </DialogDescription>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void act(submitName);
            }}
            className="grid gap-4"
          >
            <Field id="file-name" label={nameAction === "rename" ? "変更後の名前" : "名前"}>
              <Input
                id="file-name"
                autoFocus
                value={name}
                required
                disabled={busy}
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            {error && <ErrorText>{error}</ErrorText>}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={busy}
                onClick={() => setNameAction(null)}
              >
                キャンセル
              </Button>
              <Button
                type="submit"
                disabled={
                  busy || !name.trim() || (nameAction === "rename" && name === selected?.info.name)
                }
              >
                {nameAction === "rename" ? "変更" : "作成"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog
        open={moving}
        onOpenChange={(open) => {
          if (!open && !busy) setMoving(false);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>移動先を選択</DialogTitle>
            <DialogDescription>
              {selected?.info.name}{" "}
              を移動するフォルダを開いてください。同じ名前がある場所には移動できません。
            </DialogDescription>
          </DialogHeader>
          <Breadcrumbs
            root={root}
            path={movePath}
            disabled={busy}
            onNavigate={(next) => void act(() => browseMove(next))}
          />
          <div className="max-h-64 min-h-32 overflow-y-auto rounded-lg border p-2">
            <Button
              variant="ghost"
              className="w-full justify-start"
              disabled={busy || movePath === "."}
              onClick={() => void act(() => browseMove(parent(movePath)))}
            >
              <ArrowUpIcon />
              上の階層へ
            </Button>
            {moveEntries
              .filter(
                (entry) =>
                  !selected?.info.directory || join(movePath, entry.name) !== selected.path,
              )
              .map((entry) => (
                <Button
                  key={entry.name}
                  variant="ghost"
                  className="h-auto min-h-9 w-full justify-start text-left whitespace-normal break-all"
                  disabled={busy}
                  onClick={() => void act(() => browseMove(join(movePath, entry.name)))}
                >
                  <FolderIcon />
                  {entry.name}
                  <ChevronRightIcon className="ml-auto" />
                </Button>
              ))}
            {!moveEntries.length && (
              <p className="p-3 text-xs text-muted-foreground">サブフォルダはありません</p>
            )}
          </div>
          {error && <ErrorText>{error}</ErrorText>}
          <DialogFooter>
            <Button variant="outline" disabled={busy} onClick={() => setMoving(false)}>
              キャンセル
            </Button>
            <Button disabled={busy || moveInvalid} onClick={() => void act(move)}>
              ここに移動
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      {manager && (
        <details className="rounded-xl border">
          <summary className="cursor-pointer px-4 py-3 text-sm text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring">
            公開ディレクトリの権限設定
          </summary>
          <fieldset disabled={busy} className="grid max-w-2xl gap-4 border-t p-4">
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
          </fieldset>
        </details>
      )}
    </section>
  );
}
