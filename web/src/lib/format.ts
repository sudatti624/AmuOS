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
