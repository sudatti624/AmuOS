import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { request as httpsRequest } from "node:https";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { expect, type Page } from "@playwright/test";

const run = promisify(execFile);
const root = resolve(import.meta.dirname, "..", "..");
const exe = process.platform === "win32" ? ".exe" : "";
const binary = process.env.AMUOS_E2E_BINARY ?? join(root, "dist", `controlplane${exe}`);
const devcert = process.env.AMUOS_E2E_DEVCERT ?? join(root, "dist", `devcert${exe}`);

export const ownerPassword = "correct horse battery staple";

export interface ControlPlane {
  origin: string;
  setupToken: string;
  backupAndRestore: () => Promise<void>;
  stop: () => Promise<void>;
}

function freePort() {
  return new Promise<number>((done, fail) => {
    const server = createServer();
    server.once("error", fail);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      server.close(() =>
        typeof address === "object" && address ? done(address.port) : fail(new Error("no port")),
      );
    });
  });
}

// Minimal HTTPS client for the self-signed test certificate.
export function call(origin: string, method: string, path: string, body?: unknown) {
  return new Promise<{ status: number; body: string }>((done, fail) => {
    const data = body === undefined ? undefined : JSON.stringify(body);
    const req = httpsRequest(
      `${origin}${path}`,
      {
        method,
        rejectUnauthorized: false,
        headers: { Origin: origin, ...(data ? { "Content-Type": "application/json" } : {}) },
      },
      (res) => {
        let text = "";
        res.on("data", (chunk: Buffer) => (text += chunk.toString()));
        res.on("end", () => done({ status: res.statusCode ?? 0, body: text }));
      },
    );
    req.on("error", fail);
    req.end(data);
  });
}

// Starts the real control plane binary with a new database and certificate.
export async function startControlPlane(): Promise<ControlPlane> {
  const dir = await mkdtemp(join(tmpdir(), "amuos-e2e-"));
  await run(devcert, ["--dir", dir]);
  const db = join(dir, "amuos.db");
  const tokenFile = join(dir, "setup-token");
  await run(binary, ["--db", db, "--bootstrap-file", tokenFile]);
  const setupToken = (await readFile(tokenFile, "utf8")).trim();
  const port = await freePort();
  const origin = `https://localhost:${port}`;
  let child: ChildProcess;
  const launch = async () => {
    child = spawn(
      binary,
      [
        "--db",
        db,
        "--addr",
        `127.0.0.1:${port}`,
        "--origin",
        origin,
        "--tls-cert",
        join(dir, "cert.pem"),
        "--tls-key",
        join(dir, "key.pem"),
      ],
      { stdio: "ignore" },
    );
    const deadline = Date.now() + 15_000;
    for (;;) {
      try {
        if ((await call(origin, "GET", "/healthz")).status === 200) break;
      } catch {
        // Not listening yet.
      }
      if (Date.now() > deadline) {
        child.kill();
        throw new Error("control plane did not start");
      }
      await new Promise((r) => setTimeout(r, 100));
    }
  };
  const stopProcess = async () => {
    if (child.exitCode === null && child.signalCode === null) {
      const exited = new Promise((r) => child.once("exit", r));
      child.kill();
      await exited;
    }
  };
  await launch();
  return {
    origin,
    setupToken,
    backupAndRestore: async () => {
      const identity = join(dir, "recovery.key");
      const archive = join(dir, "snapshot.age");
      const { stdout } = await run(binary, ["--generate-recovery-key", identity]);
      const recipient = stdout.trim();
      await expect(
        run(binary, ["--db", db, "--backup-out", archive, "--recipient", recipient]),
      ).rejects.toThrow();
      await stopProcess();
      await run(binary, [
        "--db",
        db,
        "--backup-out",
        archive,
        "--recipient",
        recipient,
        "--addr",
        `127.0.0.1:${port}`,
        "--origin",
        origin,
      ]);
      await run(binary, ["--db", db, "--restore-from", archive, "--recovery-key-file", identity]);
      await launch();
    },
    stop: async () => {
      await stopProcess();
      await rm(dir, { recursive: true, force: true, maxRetries: 5 });
    },
  };
}

export async function setupOwner(server: ControlPlane, username = "owner") {
  const result = await call(server.origin, "POST", "/api/v1/setup", {
    token: server.setupToken,
    username,
    password: ownerPassword,
  });
  expect(result.status).toBe(201);
}

export async function login(page: Page, origin: string, username: string, password: string) {
  await page.goto(`${origin}/login`);
  await page.getByLabel("ユーザー名").fill(username);
  await page.getByLabel("パスワード").fill(password);
  await page.getByRole("button", { name: "ログイン" }).click();
  await expect(page.getByRole("heading", { name: "概要" })).toBeVisible();
}

// Fails the test if the strict Content-Security-Policy blocked anything.
export function watchConsole(page: Page) {
  const problems: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error" && /Content Security Policy|Refused to/i.test(message.text()))
      problems.push(message.text());
  });
  page.on("pageerror", (error) => problems.push(error.message));
  return () => expect(problems).toEqual([]);
}

export function nav(page: Page) {
  return page.getByRole("navigation", { name: "メイン" });
}
