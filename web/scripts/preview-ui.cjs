// Development preview of the web UI with sample data.
//
// Serves the built UI (internal/webui/static/app) and answers the API from an
// in-memory sample. No control plane, Agent or database is started, nothing on
// the machine is read or changed, and every value is synthetic. It listens on
// 127.0.0.1 only and has no authentication; it is not for real use.
//
// Usage, from web/ after `pnpm exec vp build`:
//   node scripts/preview-ui.cjs [port] [--login]
// Each page load opens signed in as demo-owner; --login keeps the sign-in
// screen, where any username and password are accepted.
const http = require("node:http");
const fs = require("node:fs/promises");
const path = require("node:path");
const crypto = require("node:crypto");

const root = path.resolve(__dirname, "../..");
const app = path.join(root, "internal/webui/static/app");
const args = process.argv.slice(2);
const showLogin = args.includes("--login");
const port = Number(args.find((a) => /^\d+$/.test(a)) || process.env.PORT || 5180);
// Runs before the app (a classic script ahead of the module), so the session
// already holds a CSRF token and the app opens signed in. Served from the same
// origin, so the strict CSP still applies unchanged.
const autoLogin =
  "try { if (!sessionStorage.getItem('amuos.csrf')) sessionStorage.setItem('amuos.csrf', 'preview-csrf'); } catch {}\n";
const GiB = 1024 ** 3;
const nowSec = () => Math.floor(Date.now() / 1000);

let loggedIn = true;
const me = {
  id: "demo-owner",
  username: "demo-owner",
  grants: [{ role: "Owner", all_hosts: true }],
  authorization_version: 1,
};
const agent = (id, extra = {}) => ({
  id,
  version: "v0.7-dev",
  capabilities: ["heartbeat", "metrics", "host_info", "tasks", "files_v1"],
  last_seen: nowSec(),
  connected: true,
  credential_expires_at: nowSec() + 86400 * 60,
  credential_renewal_due: false,
  ...extra,
});
const hosts = [
  { id: "ubuntu-lab", name: "ubuntu-lab", state: "active", agent: agent("a1") },
  { id: "debian-app", name: "debian-app", state: "active", agent: agent("a2") },
  { id: "windows-dev", name: "windows-dev", state: "active", agent: agent("a3") },
  {
    id: "backup-node",
    name: "backup-node",
    state: "active",
    agent: agent("a4", { connected: false }),
  },
  { id: "test-node", name: "test-node", state: "pending", agent: null },
];
const users = [
  { id: "demo-owner", username: "demo-owner", enabled: true },
  { id: "u-ops", username: "ops-member", enabled: true },
  { id: "u-old", username: "former-member", enabled: false },
];
const userGrants = {
  "demo-owner": me.grants,
  "u-ops": [{ role: "Operator", all_hosts: true }],
  "u-old": [],
};
const groups = [
  {
    id: "g-admins",
    name: "admins",
    user_ids: ["u-ops"],
    grants: [{ role: "Admin", host_id: "ubuntu-lab", all_hosts: false }],
  },
];
const invitations = [];
const applications = new Map();
for (const h of hosts) applications.set(h.id, []);
applications.set("ubuntu-lab", [
  {
    name: "hello-amuos",
    revision: 2,
    config: { runtime: "python", read_roles: ["Admin", "Operator", "Viewer"], operate_roles: [] },
  },
  {
    name: "status-page",
    revision: 1,
    config: { runtime: "node", read_roles: ["Admin", "Operator"], operate_roles: [] },
  },
]);
const fileRoots = new Map(
  hosts.map((h) => [
    h.id,
    [
      {
        alias: "workspace",
        read_roles: ["Admin", "Operator", "Viewer"],
        write_roles: ["Admin", "Operator"],
      },
    ],
  ]),
);
const sampleText =
  '# Sample application configuration\n\n[app]\nname = "hello-amuos"\nport = 8080\nlog_level = "info"\n\n[health]\npath = "/healthz"\ninterval_seconds = 30\n';
const files = new Map([
  ["assets", { directory: true, data: "" }],
  ["assets/logo.svg", { directory: false, data: '<svg xmlns="http://www.w3.org/2000/svg"/>\n' }],
  ["config.toml", { directory: false, data: sampleText }],
  ["README.md", { directory: false, data: "# hello-amuos\n\nSample files for the UI preview.\n" }],
]);
const tasks = [];
const fileResults = new Map();
let monitoring = { sample_interval_seconds: 15, raw_retention_days: 7, rollup_retention_days: 30 };
let retention = {
  retention_days: 90,
  automatic_enabled: true,
  next_run_at: nowSec() + 3600,
  last_run_at: nowSec() - 82800,
  failure_count: 0,
};
const audit = [];
function record(action, target = "") {
  audit.unshift({
    id: audit.length + 1,
    at: nowSec(),
    actor_id: "demo-owner",
    action,
    target_id: target,
    host_id: target,
    outcome: "success",
    reason: "",
    request_id: crypto.randomUUID(),
    details: {},
  });
}
["auth.login", "host.create", "agent.enroll", "task.result", "file.request"].forEach((a, i) =>
  audit.push({
    id: 100 - i,
    at: nowSec() - 600 - i * 180,
    actor_id: "demo-owner",
    action: a,
    target_id: "ubuntu-lab",
    host_id: "ubuntu-lab",
    outcome: "success",
    reason: "",
    request_id: `sample-${i}`,
    details: {},
  }),
);

function wave(base, i, n) {
  return Math.max(0, base * (1 + Math.sin(i * 0.19 + n) * 0.14 + Math.cos(i * 0.49 + n) * 0.06));
}
const bases = {
  cpu: 24,
  load1: 1.2,
  load5: 1.06,
  load15: 0.9,
  memory: 46,
  swap: 2.4,
  disk: 38.8,
  disk_read: 6 * 1024 ** 2,
  disk_write: 2.8 * 1024 ** 2,
  net_rx: 1.4 * 1024 ** 2,
  net_tx: 760 * 1024,
};
function history(range) {
  const step = { "1h": 15, "6h": 60, "24h": 300, "7d": 3600, "30d": 7200 }[range] || 15;
  const span =
    { "1h": 3600, "6h": 21600, "24h": 86400, "7d": 604800, "30d": 2592000 }[range] || 3600;
  const to = Math.floor(nowSec() / step) * step;
  const count = Math.floor(span / step) + 1;
  const times = Array.from({ length: count }, (_, i) => to - (count - 1 - i) * step);
  const series = {};
  Object.entries(bases).forEach(([name, base], n) => {
    const avg = times.map((t) => wave(base, t / step, n));
    series[name] = { avg, min: avg.map((v) => v * 0.88), max: avg.map((v) => v * 1.12) };
  });
  return { range, from: times[0], to, step, times, series };
}
function detail(host) {
  const t = nowSec();
  const windows = host.id === "windows-dev";
  const live = host.state === "active";
  const latest = live
    ? {
        seq: 240,
        collected_at: t,
        received_at: t,
        current: host.agent?.connected !== false,
        uptime: 12 * 86400 + 4 * 3600,
        ...Object.fromEntries(Object.entries(bases).map(([k, v], n) => [k, wave(v, t / 15, n)])),
      }
    : null;
  if (latest && windows) {
    latest.load1 = latest.load5 = latest.load15 = null;
    latest.swap = null;
  }
  const info = live
    ? {
        collected_at: t,
        received_at: t,
        hostname: host.name,
        os: windows ? "Windows" : "Ubuntu 24.04 LTS",
        kernel: windows ? "10.0.26100" : "6.8.0-generic",
        arch: "amd64",
        cpu_model: "Sample 8-core CPU",
        cpu_count: 8,
        memory_bytes: 16 * GiB,
        swap_bytes: windows ? null : 4 * GiB,
        boot_time: t - 86400 * 12,
        filesystems: windows
          ? [{ mount: "C:\\", type: "NTFS", total_bytes: 476 * GiB, used_bytes: 212 * GiB }]
          : [
              { mount: "/", type: "ext4", total_bytes: 240 * GiB, used_bytes: 93 * GiB },
              { mount: "/srv", type: "ext4", total_bytes: 500 * GiB, used_bytes: 176 * GiB },
            ],
        interfaces: [
          {
            name: windows ? "Ethernet" : "eth0",
            addresses: ["192.0.2." + (10 + hosts.indexOf(host)) + "/24"],
          },
        ],
      }
    : null;
  return { ...host, sample_interval: monitoring.sample_interval_seconds, latest, info };
}
function newTask(hostId, operation, state = "succeeded") {
  const t = nowSec();
  const task = {
    id: crypto.randomUUID(),
    operation,
    host_id: hostId,
    state,
    attempt: 1,
    reason: "",
    created_at: t,
    updated_at: t,
    finished_at: state === "succeeded" ? t : null,
    retry_of: null,
    retried_by: null,
  };
  tasks.unshift(task);
  return task;
}
for (let i = 0; i < 3; i++)
  Object.assign(newTask("ubuntu-lab", i ? "collect" : "file"), {
    created_at: nowSec() - 90 - i * 180,
    finished_at: nowSec() - 80 - i * 180,
  });

const entry = (name, f) => ({
  name: path.posix.basename(name),
  directory: f.directory,
  size: f.directory ? 4096 : Buffer.byteLength(f.data),
  modified: f.modified || nowSec() - 300,
  etag: crypto
    .createHash("sha256")
    .update(name + f.data)
    .digest("hex"),
});
function fileOp(q) {
  const p = q.path === "." ? "" : q.path.replace(/^\.\//, "");
  const f = files.get(p);
  switch (q.action) {
    case "list": {
      const prefix = p ? p + "/" : "";
      const entries = [...files]
        .filter(([k]) => k.startsWith(prefix) && k !== p && !k.slice(prefix.length).includes("/"))
        .map(([k, v]) => entry(k, v));
      return { entries };
    }
    case "stat":
      return f ? { info: entry(p, f) } : { error: "not_found" };
    case "read":
      return f && !f.directory
        ? { info: entry(p, f), data: Buffer.from(f.data).toString("base64") }
        : { error: "not_found" };
    case "write":
      f.data = Buffer.from(q.data, "base64").toString();
      f.modified = nowSec();
      return { info: entry(p, f) };
    case "create":
      if (files.has(p)) return { error: "exists" };
      files.set(p, {
        directory: false,
        data: q.data ? Buffer.from(q.data, "base64").toString() : "",
      });
      return {};
    case "mkdir":
      if (files.has(p)) return { error: "exists" };
      files.set(p, { directory: true, data: "" });
      return {};
    case "move":
      files.delete(p);
      files.set(q.destination, f);
      return {};
    case "delete":
      if (f?.directory && [...files.keys()].some((k) => k.startsWith(p + "/")))
        return { error: "not_empty" };
      files.delete(p);
      return {};
    default:
      return { error: "unsupported" };
  }
}

function route(method, p, body) {
  if (p === "/auth/login" && method === "POST") {
    loggedIn = true;
    record("auth.login");
    return [200, { user_id: me.id, csrf_token: "preview-csrf" }];
  }
  if (p === "/auth/reauthenticate") return [204];
  if (!loggedIn) return [401, { error: "unauthorized" }];
  if (p === "/auth/logout") {
    loggedIn = false;
    return [204];
  }
  if (p === "/me") return [200, me];
  if (p === "/audit") return [200, { items: audit }];
  if (p === "/audit/retention") {
    if (method === "PUT") retention = { ...retention, ...body };
    return [200, retention];
  }
  if (p === "/audit/retention/run")
    return [
      200,
      {
        retention_days: retention.retention_days,
        cutoff: nowSec() - retention.retention_days * 86400,
        deleted_count: 0,
        more_expired: false,
      },
    ];
  if (p === "/monitoring/settings") {
    if (method === "PUT") monitoring = body;
    return [200, monitoring];
  }
  if (p === "/users" && method === "GET") return [200, { items: users }];
  let m;
  if ((m = p.match(/^\/users\/([^/]+)\/grants$/))) {
    if (method === "PUT") userGrants[m[1]] = body.grants;
    return [200, { items: userGrants[m[1]] || [] }];
  }
  if (p.startsWith("/users/")) return [204];
  if (p === "/groups") {
    if (method === "POST") {
      const g = { id: crypto.randomUUID(), name: body.name, user_ids: [], grants: [] };
      groups.push(g);
      return [201, g];
    }
    return [200, { items: groups }];
  }
  if ((m = p.match(/^\/groups\/([^/]+)(?:\/(members|grants))?$/))) {
    const g = groups.find((x) => x.id === m[1]);
    if (method === "DELETE") {
      groups.splice(groups.indexOf(g), 1);
      return [204];
    }
    if (m[2] === "members") g.user_ids = body.user_ids;
    else if (m[2] === "grants") g.grants = body.grants;
    return [200, g];
  }
  if (p === "/invitations" && method === "POST") {
    const inv = {
      id: crypto.randomUUID(),
      token: "preview-invitation-" + crypto.randomBytes(8).toString("hex"),
      username: body.username,
      expires_at: nowSec() + 86400,
    };
    invitations.push(inv);
    return [201, inv];
  }
  if (p.startsWith("/invitations/") || p === "/ownership/transfer") return [204];
  if (p === "/hosts") {
    if (method === "POST") {
      const h = {
        id: body.name.toLowerCase().replace(/[^a-z0-9-]/g, "-"),
        name: body.name,
        state: "pending",
        agent: null,
      };
      hosts.push(h);
      applications.set(h.id, []);
      fileRoots.set(h.id, []);
      record("host.create", h.id);
      return [201, h];
    }
    return [200, { items: hosts }];
  }
  if (p === "/tasks") return [200, { items: tasks }];
  if ((m = p.match(/^\/tasks\/([^/]+)(?:\/(file-result|cancel|resolve|retry))?$/))) {
    const t = tasks.find((x) => x.id === m[1]);
    if (!t) return [404, { error: "not_found" }];
    if (m[2] === "file-result") return [200, fileResults.get(t.id)];
    if (m[2] === "cancel") {
      t.state = "cancelled";
      return [200, t];
    }
    if (m[2] === "resolve") {
      t.state = body.resolution;
      return [200, t];
    }
    if (m[2] === "retry") {
      const n = newTask(t.host_id, t.operation);
      n.retry_of = t.id;
      t.retried_by = n.id;
      return [201, n];
    }
    return [200, t];
  }
  if ((m = p.match(/^\/hosts\/([^/]+)(\/.*)?$/))) {
    const host = hosts.find((h) => h.id === m[1]);
    if (!host) return [404, { error: "not_found" }];
    const sub = m[2] || "";
    if (sub === "" && method === "DELETE") {
      hosts.splice(hosts.indexOf(host), 1);
      return [204];
    }
    if (sub === "") return [200, detail(host)];
    if (sub === "/metrics") return [200, history(new URLSearchParams(body).get("range"))];
    if (sub === "/revoke") {
      host.state = "revoked";
      return [204];
    }
    if (sub === "/enrollment")
      return [
        201,
        {
          id: crypto.randomUUID(),
          host_id: host.id,
          code: "preview." + crypto.randomBytes(12).toString("base64url"),
          expires_at: nowSec() + 600,
        },
      ];
    if (sub === "/tasks") {
      if (method === "POST") return [201, newTask(host.id, "collect")];
      return [200, { items: tasks.filter((t) => t.host_id === host.id) }];
    }
    if (sub === "/systemd") {
      if (host.state !== "active" || host.id === "windows-dev") return [200, null];
      const units = [
        ["amuos-agent.service", "active", "running"],
        ["nginx.service", "active", "running"],
        ["ssh.service", "active", "running"],
        ["cron.service", "active", "running"],
        ["backup.service", "failed", "failed"],
        ["apt-daily.service", "inactive", "dead"],
      ];
      return [
        200,
        {
          collected_at: nowSec(),
          received_at: nowSec(),
          available: true,
          truncated: false,
          units: units.map(([unit, active, s]) => ({ unit, load: "loaded", active, sub: s })),
        },
      ];
    }
    if (sub === "/file-roots") {
      if (method === "PUT") fileRoots.set(host.id, body.roots);
      return [200, { items: fileRoots.get(host.id) }];
    }
    if (sub === "/files" && method === "POST") {
      const t = newTask(host.id, "file");
      fileResults.set(t.id, fileOp(body));
      return [201, t];
    }
    if (sub.startsWith("/applications")) {
      const list = applications.get(host.id);
      const name = sub.split("/")[2];
      if (!name) return [200, { items: list }];
      const i = list.findIndex((a) => a.name === name);
      if (method === "DELETE") {
        list.splice(i, 1);
        return [204];
      }
      if (method === "PUT") {
        const a = { name, revision: (list[i]?.revision || 0) + 1, config: body.config };
        if (i >= 0) list[i] = a;
        else list.push(a);
        return [200, a];
      }
      return i >= 0 ? [200, list[i]] : [404, { error: "not_found" }];
    }
  }
  return null;
}

const mime = {
  ".js": "text/javascript",
  ".css": "text/css",
  ".png": "image/png",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
  ".html": "text/html; charset=utf-8",
};
const server = http.createServer(async (req, res) => {
  const u = new URL(req.url, "http://localhost");
  if (u.pathname.startsWith("/api/v1/")) {
    let raw = "";
    for await (const chunk of req) raw += chunk;
    let body = raw ? JSON.parse(raw) : undefined;
    const p = u.pathname.slice("/api/v1".length);
    if (p.endsWith("/metrics")) body = u.search;
    const result = route(req.method, p, body);
    if (!result) {
      console.warn("no sample response:", req.method, p);
      res.writeHead(404, { "Content-Type": "application/json" });
      return res.end('{"error":"not_found"}');
    }
    const [status, data] = result;
    res.writeHead(status, data === undefined ? {} : { "Content-Type": "application/json" });
    return res.end(data === undefined ? "" : JSON.stringify(data));
  }
  if (u.pathname === "/preview-login.js") {
    res.writeHead(200, { "Content-Type": "text/javascript", "Cache-Control": "no-store" });
    return res.end(autoLogin);
  }
  const asset = u.pathname.startsWith("/assets/") || u.pathname === "/favicon.png";
  const file = asset
    ? path.join(app, path.normalize(u.pathname).replace(/^[/\\]+/, ""))
    : path.join(app, "index.html");
  if (!file.startsWith(app)) {
    res.writeHead(400);
    return res.end();
  }
  try {
    let data = await fs.readFile(file);
    const nonce = crypto.randomBytes(16).toString("base64");
    if (!asset) {
      let html = data.toString().replaceAll("AMUOS_CSP_NONCE", nonce);
      if (!showLogin) {
        loggedIn = true;
        html = html.replace("<head>", '<head>\n    <script src="/preview-login.js"></script>');
      }
      data = Buffer.from(html);
    }
    res.writeHead(200, {
      "Content-Type": mime[path.extname(file)] || "application/octet-stream",
      "Content-Security-Policy": `default-src 'none'; script-src 'self'; style-src 'self' 'nonce-${nonce}'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`,
      "Cache-Control": "no-store",
    });
    res.end(data);
  } catch {
    res.writeHead(asset ? 404 : 503, { "Content-Type": "text/plain; charset=utf-8" });
    res.end(asset ? "not found" : "Build the web UI first: pnpm exec vp build (in web/)");
  }
});
server.listen(port, "127.0.0.1", () => {
  console.log(`AmuOS UI preview (sample data only): http://127.0.0.1:${port}/`);
  console.log(
    showLogin
      ? "Sign in with any username and password. Ctrl+C to stop."
      : "Opens signed in as demo-owner (add --login to show the sign-in screen). Ctrl+C to stop.",
  );
});
