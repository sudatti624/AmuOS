import { ChevronRightIcon, FileIcon, FolderIcon, HardDriveIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatBytes, formatTime } from "@/lib/format";

export interface FileEntry {
  name: string;
  directory: boolean;
  size: number;
  modified: number;
  etag: string;
}
const join = (path: string, name: string) => (path === "." ? name : `${path}/${name}`);
export function Breadcrumbs({
  root,
  path,
  disabled,
  onNavigate,
}: {
  root: string;
  path: string;
  disabled: boolean;
  onNavigate: (path: string) => void;
}) {
  const parts = path === "." ? [] : path.split("/");
  return (
    <nav aria-label="フォルダの場所" className="flex min-w-0 flex-wrap items-center gap-1 text-sm">
      <Button variant="ghost" size="sm" disabled={disabled} onClick={() => onNavigate(".")}>
        <HardDriveIcon />
        {root}
      </Button>
      {parts.map((part, i) => (
        <span key={parts.slice(0, i + 1).join("/")} className="flex min-w-0 items-center gap-1">
          <ChevronRightIcon aria-hidden className="size-3 shrink-0 text-subtle" />
          <Button
            variant="ghost"
            size="sm"
            className="min-w-0 whitespace-normal break-all"
            disabled={disabled}
            aria-current={i === parts.length - 1 ? "page" : undefined}
            onClick={() => onNavigate(parts.slice(0, i + 1).join("/"))}
          >
            {part}
          </Button>
        </span>
      ))}
    </nav>
  );
}

export function FileListing({
  entries,
  selectedPath,
  path,
  view,
  onSelect,
  onToggle,
  onOpen,
}: {
  entries: FileEntry[];
  selectedPath?: string;
  path: string;
  view: "list" | "grid";
  onSelect: (entry: FileEntry) => void;
  onToggle: (entry: FileEntry) => void;
  onOpen: (entry: FileEntry) => void;
}) {
  return view === "list" ? (
    <Table className="[&_td]:py-2.5">
      <TableHeader>
        <TableRow>
          <TableHead className="w-10">
            <span className="sr-only">選択</span>
          </TableHead>
          <TableHead>名前</TableHead>
          <TableHead className="hidden text-right sm:table-cell">サイズ</TableHead>
          <TableHead className="hidden md:table-cell">更新日時</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {entries.map((entry) => {
          const current = selectedPath === join(path, entry.name);
          const Icon = entry.directory ? FolderIcon : FileIcon;
          return (
            <TableRow
              key={entry.name}
              className={`cursor-pointer ${current ? "bg-accent" : ""}`}
              onClick={() => onSelect(entry)}
              onDoubleClick={() => onOpen(entry)}
            >
              <TableCell>
                <input
                  type="checkbox"
                  aria-label={`${entry.name} を選択`}
                  checked={current}
                  onClick={(e) => e.stopPropagation()}
                  onChange={() => onToggle(entry)}
                />
              </TableCell>
              <TableCell>
                <button
                  type="button"
                  className="flex min-h-7 w-full items-center gap-3 rounded-sm text-left text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  onClick={(e) => {
                    e.stopPropagation();
                    onOpen(entry);
                  }}
                >
                  <Icon
                    aria-hidden
                    className={`size-5 shrink-0 ${entry.directory ? "fill-muted text-muted-foreground" : "text-subtle"}`}
                  />
                  <span className="break-all">{entry.name}</span>
                  {entry.directory && (
                    <ChevronRightIcon
                      aria-hidden
                      className="ml-auto size-3.5 shrink-0 text-subtle"
                    />
                  )}
                </button>
              </TableCell>
              <TableCell className="hidden text-right text-xs text-muted-foreground tabular-nums sm:table-cell">
                {entry.directory ? "—" : formatBytes(entry.size)}
              </TableCell>
              <TableCell className="stamp hidden whitespace-nowrap md:table-cell">
                {formatTime(entry.modified)}
              </TableCell>
            </TableRow>
          );
        })}
      </TableBody>
    </Table>
  ) : (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-4">
      {entries.map((entry) => {
        const current = selectedPath === join(path, entry.name);
        const Icon = entry.directory ? FolderIcon : FileIcon;
        return (
          <div
            key={entry.name}
            className={`relative rounded-lg border p-3 transition-colors hover:bg-muted/40 ${current ? "border-foreground/40 bg-accent" : "border-transparent"}`}
            onClick={() => onSelect(entry)}
            onDoubleClick={() => onOpen(entry)}
          >
            <input
              type="checkbox"
              className="absolute top-3 left-3"
              aria-label={`${entry.name} を選択`}
              checked={current}
              onClick={(e) => e.stopPropagation()}
              onChange={() => onToggle(entry)}
            />
            <button
              type="button"
              className="flex w-full flex-col items-center gap-3 rounded-md px-1 pt-5 pb-2 text-center text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
              onClick={(e) => {
                e.stopPropagation();
                onOpen(entry);
              }}
            >
              <Icon
                aria-hidden
                className={`size-10 ${entry.directory ? "fill-muted text-muted-foreground" : "text-subtle"}`}
              />
              <span className="w-full break-all">{entry.name}</span>
            </button>
            <p className="text-center text-xs text-subtle">
              {entry.directory ? "フォルダ" : formatBytes(entry.size)}
            </p>
          </div>
        );
      })}
    </div>
  );
}
