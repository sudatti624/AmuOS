// Typed client for the control plane API. Authorization is always decided by
// the server; the UI only hides actions the current grants cannot perform.

export type Role = "Owner" | "Admin" | "Operator" | "Viewer";

export interface Grant {
  role: Role;
  all_hosts: boolean;
  host_id?: string;
}

export interface Me {
  id: string;
  username: string;
  grants: Grant[];
  authorization_version: number;
}

export interface User {
  id: string;
  username: string;
  enabled: boolean;
}

export interface Group {
  id: string;
  name: string;
  user_ids: string[];
  grants: Grant[];
}

export interface AgentInfo {
  id: string;
  version: string;
  capabilities: string[];
  last_seen: number | null;
  connected: boolean;
  credential_expires_at: number;
  credential_renewal_due: boolean;
}

export interface Host {
  id: string;
  name: string;
  state: string;
  agent?: AgentInfo | null;
}

export interface Filesystem {
  mount: string;
  type: string;
  total_bytes: number | null;
  used_bytes: number | null;
}

export interface NetworkInterface {
  name: string;
  addresses: string[];
}

// Empty text and null numbers mean the Agent could not read the value.
export interface HostInfo {
  collected_at: number;
  received_at: number;
  hostname: string;
  os: string;
  kernel: string;
  arch: string;
  cpu_model: string;
  cpu_count: number | null;
  memory_bytes: number | null;
  swap_bytes: number | null;
  boot_time: number | null;
  filesystems: Filesystem[];
  interfaces: NetworkInterface[];
}

export const metricNames = [
  "cpu",
  "load1",
  "load5",
  "load15",
  "memory",
  "swap",
  "disk",
  "disk_read",
  "disk_write",
  "net_rx",
  "net_tx",
] as const;

export type MetricName = (typeof metricNames)[number];

export type Sample = { [K in MetricName]: number | null } & {
  seq: number;
  collected_at: number;
  received_at: number;
  uptime: number | null;
  // False once the value is too old to show as the present state.
  current: boolean;
};

export interface HostDetail extends Host {
  info: HostInfo | null;
  latest: Sample | null;
  sample_interval: number;
}

export interface Series {
  avg: (number | null)[];
  min: (number | null)[];
  max: (number | null)[];
}

// Only steps with data are listed; a missing step is a gap.
export interface History {
  range: string;
  step: number;
  from: number;
  to: number;
  times: number[];
  series: Record<MetricName, Series>;
}

export interface Enrollment {
  id: string;
  host_id: string;
  code: string;
  expires_at: number;
}

export interface AuditEvent {
  id: number;
  at: number;
  actor_id: string;
  action: string;
  target_id: string;
  host_id?: string;
  outcome: string;
  reason: string;
  request_id?: string;
  details: unknown;
}

export interface Invitation {
  id: string;
  token: string;
  username: string;
  expires_at: number;
}

export interface Retention {
  retention_days: number;
  automatic_enabled: boolean;
  next_run_at: number;
  last_run_at: number | null;
  failure_count: number;
  last_error_code?: string;
}

export interface RetentionRun {
  retention_days: number;
  cutoff: number;
  deleted_count: number;
  more_expired: boolean;
}

export interface Items<T> {
  items: T[];
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly requestId: string | null;

  constructor(status: number, code: string, requestId: string | null) {
    super(code);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.requestId = requestId;
  }
}

// The CSRF token is returned only by login and the server stores only its hash,
// so it is kept for this tab. A new tab needs a new login before changing state.
const csrfKey = "amuos.csrf";

export function getCsrf(): string | null {
  try {
    return sessionStorage.getItem(csrfKey);
  } catch {
    return null;
  }
}

export function setCsrf(token: string | null) {
  try {
    if (token) sessionStorage.setItem(csrfKey, token);
    else sessionStorage.removeItem(csrfKey);
  } catch {
    // Storage can be unavailable; mutations then require a new login.
  }
}

let unauthorizedHandler: (() => void) | null = null;

export function onUnauthorized(handler: (() => void) | null) {
  unauthorizedHandler = handler;
}

type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

export interface RequestOptions {
  body?: unknown;
  idempotencyKey?: string;
  // Credential checks (login, re-authentication) answer 401 for a wrong
  // password without ending the session.
  credentialCheck?: boolean;
}

export async function request<T>(
  method: Method,
  path: string,
  options: RequestOptions = {},
): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (options.body !== undefined) headers["Content-Type"] = "application/json";
  if (method !== "GET") {
    const csrf = getCsrf();
    if (csrf) headers["X-CSRF-Token"] = csrf;
  }
  if (options.idempotencyKey) headers["Idempotency-Key"] = options.idempotencyKey;
  let response: Response;
  try {
    response = await fetch(`/api/v1${path}`, {
      method,
      headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      credentials: "same-origin",
      cache: "no-store",
    });
  } catch {
    throw new ApiError(0, "network_error", null);
  }
  const requestId = response.headers.get("X-Request-ID");
  if (response.status === 204) return undefined as T;
  let data: unknown = null;
  try {
    data = await response.json();
  } catch {
    data = null;
  }
  if (!response.ok) {
    const code =
      typeof data === "object" && data !== null && "error" in data && typeof data.error === "string"
        ? data.error
        : "unknown_error";
    if (response.status === 401 && !options.credentialCheck) unauthorizedHandler?.();
    throw new ApiError(response.status, code, requestId);
  }
  return data as T;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body: unknown, options?: Omit<RequestOptions, "body">) =>
    request<T>("POST", path, { ...options, body }),
  put: <T>(path: string, body: unknown, options?: Omit<RequestOptions, "body">) =>
    request<T>("PUT", path, { ...options, body }),
  patch: <T>(path: string, body: unknown) => request<T>("PATCH", path, { body }),
  delete: (path: string) => request<void>("DELETE", path),
};

const messages: Record<string, string> = {
  network_error: "サーバーに接続できませんでした。通信状態を確認してください。",
  unauthorized: "認証できませんでした。入力内容を確認してください。",
  forbidden: "この操作を行う権限がありません。",
  origin_rejected:
    "このアドレスからの操作は許可されていません。サーバーの --origin 設定を確認してください。",
  invalid_input: "入力内容が正しくありません。",
  invalid_json: "送信内容の形式が正しくありません。",
  json_required: "送信内容の形式が正しくありません。",
  idempotency_key_required: "要求を識別できませんでした。もう一度お試しください。",
  not_found: "対象が見つかりませんでした。すでに削除されている可能性があります。",
  method_not_allowed: "この操作には対応していません。",
  conflict: "現在の状態ではこの操作を行えません。",
  rate_limited: "試行回数の上限に達しました。15分ほど待ってからお試しください。",
  auth_busy: "サーバーが混み合っています。少し待ってからお試しください。",
  audit_unavailable: "監査ログを保存できないため、操作を完了できませんでした。",
  internal_error: "サーバーでエラーが発生しました。",
};

export function errorMessage(
  error: unknown,
  overrides: Partial<Record<string, string>> = {},
): string {
  if (!(error instanceof ApiError)) return "予期しないエラーが発生しました。";
  const message =
    overrides[error.code] ?? messages[error.code] ?? `エラーが発生しました（${error.code}）。`;
  return error.status >= 500 && error.requestId
    ? `${message} リクエストID: ${error.requestId}`
    : message;
}

// Bytes, not characters, because the server limits passwords by byte length.
export function passwordBytes(password: string) {
  return new TextEncoder().encode(password).length;
}

export const usernamePattern = /^[a-z0-9_.-]{3,64}$/;
