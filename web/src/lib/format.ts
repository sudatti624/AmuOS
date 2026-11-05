import type { Grant, Host } from "@/lib/api";

const dateTime = new Intl.DateTimeFormat("ja-JP", { dateStyle: "medium", timeStyle: "short" });

export function formatTime(unixSeconds: number) {
  return dateTime.format(new Date(unixSeconds * 1000));
}

export function grantScope(grant: Grant, hosts: Host[] = []) {
  if (grant.all_hosts) return "全ホスト";
  const host = hosts.find((h) => h.id === grant.host_id);
  return host ? `ホスト: ${host.name}` : `ホスト: ${shortId(grant.host_id ?? "")}`;
}

export function grantLabel(grant: Grant, hosts: Host[] = []) {
  return `${grant.role}（${grantScope(grant, hosts)}）`;
}

export function shortId(id: string) {
  return id.length > 12 ? `${id.slice(0, 8)}…` : id;
}

export const roleDescriptions: Record<string, string> = {
  Owner: "すべての操作と、オーナー・保持設定の管理",
  Admin: "範囲内のホスト管理・監査ログ閲覧。全ホスト範囲ではユーザー管理も可能",
  Operator: "範囲内のホストの閲覧と収集操作",
  Viewer: "範囲内のホストの閲覧のみ",
};

const binaryUnits = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];

export function formatBytes(bytes: number) {
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < binaryUnits.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${unit === 0 ? value : value.toFixed(value < 10 ? 1 : 0)} ${binaryUnits[unit]}`;
}

export function formatRate(bytesPerSecond: number) {
  return `${formatBytes(bytesPerSecond)}/s`;
}

export function formatPercent(value: number) {
  if (Number.isInteger(value)) return `${value}%`;
  return `${value.toFixed(value < 10 ? 1 : 0)}%`;
}

export function formatDuration(seconds: number) {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}日 ${hours}時間`;
  if (hours > 0) return `${hours}時間 ${minutes}分`;
  return `${minutes}分`;
}

export function formatStep(seconds: number) {
  return seconds < 60
    ? `${seconds}秒`
    : seconds < 3600
      ? `${seconds / 60}分`
      : `${seconds / 3600}時間`;
}
