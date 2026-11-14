import { expect, test, type Page } from "@playwright/test";
import { startControlPlane, watchConsole, type ControlPlane } from "./support";

let server: ControlPlane;
test.beforeAll(async () => {
  server = await startControlPlane();
});
test.afterAll(async () => {
  await server.stop();
});

// These tests exercise browser interactions with deterministic task results.
// files.spec.ts separately exercises the real authorization, journal and Agent.
async function mockFiles(page: Page, readOnly = false) {
  const nodes = new Map<string, { directory: boolean; data: string }>([
    ["assets", { directory: true, data: "" }],
    ["notes", { directory: true, data: "" }],
    ["config.toml", { directory: false, data: "[app]\nname = 'demo'\n" }],
    ["README.md", { directory: false, data: "Sample readme" }],
    ["binary.dat", { directory: false, data: "\0binary" }],
    ["assets/icon.txt", { directory: false, data: "sample asset" }],
  ]);
  const calls: Array<Record<string, string | boolean>> = [];
  const jobs = new Map<string, { result: unknown; failed: boolean }>();
  let rootsLoaded = false;
  const info = (path: string) => ({
    name: path.split("/").at(-1)!,
    directory: nodes.get(path)!.directory,
    size: nodes.get(path)!.data.length,
    modified: 1794288300,
    etag: `sample-${nodes.get(path)!.data}`,
  });
  await page.addInitScript(() => sessionStorage.setItem("amuos.csrf", "sample-csrf"));
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const p = new URL(request.url()).pathname.replace("/api/v1", "");
    let data: unknown;
    if (p === "/me")
      data = {
        id: "sample-user",
        username: "demo-user",
        grants: [{ role: readOnly ? "Viewer" : "Owner", all_hosts: true }],
        authorization_version: 1,
      };
    else if (p === "/hosts/demo")
      data = {
        id: "demo",
        name: "sample-host",
        state: "pending",
        agent: null,
        info: null,
        latest: null,
        sample_interval: 15,
      };
    else if (p === "/hosts/demo/metrics")
      data = {
        range: "1h",
        from: 1794284700,
        to: 1794288300,
        step: 15,
        times: [],
        series: Object.fromEntries(
          [
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
          ].map((key) => [key, { avg: [], min: [], max: [] }]),
        ),
      };
    else if (p === "/hosts/demo/systemd") data = null;
    else if (p === "/hosts/demo/tasks" || p === "/hosts/demo/applications") data = { items: [] };
    else if (p === "/hosts/demo/file-roots") {
      rootsLoaded = true;
      data = {
        items: [{ alias: "workspace", read_roles: ["Admin", "Viewer"], write_roles: ["Admin"] }],
      };
    } else if (p === "/hosts/demo/files") {
      const q = request.postDataJSON();
      calls.push(q);
      let result: unknown = {},
        failed = false;
      if (q.action === "list") {
        const prefix = q.path === "." ? "" : `${q.path}/`;
        result = {
          entries: [...nodes.keys()]
            .filter(
              (name) =>
                name.startsWith(prefix) &&
                !name.slice(prefix.length).includes("/") &&
                name !== q.path,
            )
            .map(info),
        };
      } else if (q.action === "read")
        result = {
          info: info(q.path),
          data: Buffer.from(nodes.get(q.path)!.data).toString("base64"),
        };
      else if (q.action === "create" || q.action === "mkdir") {
        failed = nodes.has(q.path);
        if (!failed)
          nodes.set(q.path, {
            directory: q.action === "mkdir",
            data: q.data ? Buffer.from(q.data, "base64").toString() : "",
          });
      } else if (q.action === "move") {
        failed = nodes.has(q.destination);
        if (!failed) {
          nodes.set(q.destination, nodes.get(q.path)!);
          nodes.delete(q.path);
        }
      } else if (q.action === "write")
        nodes.set(q.path, { directory: false, data: Buffer.from(q.data, "base64").toString() });
      else if (q.action === "delete") nodes.delete(q.path);
      else throw new Error(`unexpected file operation ${q.action}`);
      if (failed) result = { error: "file_operation_failed" };
      const id = `sample-${jobs.size + 1}`;
      jobs.set(id, { result, failed });
      data = { id, state: "queued" };
    } else if (p.startsWith("/tasks/sample-")) {
      const id = p.split("/")[2];
      const job = jobs.get(id)!;
      data = p.endsWith("/file-result")
        ? job.result
        : { id, state: job.failed ? "failed" : "succeeded" };
    } else throw new Error(`unexpected API ${p}`);
    await route.fulfill({ status: 200, json: data });
  });
  return {
    calls,
    nodes,
    get rootsLoaded() {
      return rootsLoaded;
    },
  };
}

test("explorer navigation, selection, dialogs, dirty drafts and responsive layouts", async ({
  page,
}) => {
  const clean = watchConsole(page);
  const mock = await mockFiles(page);
  await page.goto(`${server.origin}/hosts/demo`);
  await expect.poll(() => mock.rootsLoaded).toBe(true);
  expect(mock.calls).toHaveLength(0); // Hidden tabs do not create file tasks.
  await page.getByRole("tab", { name: "ファイル", exact: true }).click();
  const files = page.getByRole("region", { name: "ファイル管理", exact: true });
  await expect(files.getByRole("button", { name: "config.toml", exact: true })).toBeVisible();
  await files.getByLabel("このフォルダ内を検索").fill("readme");
  await expect(files.getByRole("button", { name: "README.md", exact: true })).toBeVisible();
  await expect(files.getByRole("button", { name: "config.toml", exact: true })).toHaveCount(0);
  expect(mock.calls).toHaveLength(1); // Search is limited to the loaded folder.
  await files.getByLabel("このフォルダ内を検索").clear();
  await files.getByRole("button", { name: "グリッド表示" }).click();
  await expect(files.getByRole("button", { name: "グリッド表示" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await page.screenshot({ path: test.info().outputPath("explorer-grid.png"), fullPage: true });
  await files.getByRole("button", { name: "assets", exact: true }).click();
  await expect(files.getByRole("button", { name: "icon.txt", exact: true })).toBeVisible();
  await files
    .getByRole("navigation", { name: "フォルダの場所" })
    .getByRole("button", { name: "workspace", exact: true })
    .click();
  await files.getByRole("button", { name: "リスト表示" }).click();
  await files.getByRole("button", { name: "config.toml", exact: true }).click();
  await files.getByLabel("UTF-8テキスト").fill("unsaved draft");
  await files.getByRole("button", { name: "assets", exact: true }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "キャンセル" }).click();
  await expect(files.getByLabel("UTF-8テキスト")).toHaveValue("unsaved draft");
  await page.getByRole("tab", { name: "概要", exact: true }).click();
  await page.getByRole("tab", { name: "ファイル", exact: true }).click();
  await expect(files.getByLabel("UTF-8テキスト")).toHaveValue("unsaved draft");
  await files.getByRole("button", { name: "エディタを閉じる" }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "破棄する" }).click();
  await files.getByRole("button", { name: "新しいフォルダ", exact: true }).click();
  await page.getByRole("dialog").getByLabel("名前", { exact: true }).fill("documents");
  await page.getByRole("dialog").getByRole("button", { name: "作成", exact: true }).click();
  await expect(files.getByRole("button", { name: "documents", exact: true })).toBeVisible();
  await files.getByRole("checkbox", { name: "README.md を選択", exact: true }).check();
  await files.getByRole("button", { name: "名前変更", exact: true }).click();
  await page.getByRole("dialog").getByLabel("変更後の名前").fill("guide.md");
  await page.getByRole("dialog").getByRole("button", { name: "変更", exact: true }).click();
  await files.getByRole("checkbox", { name: "guide.md を選択", exact: true }).check();
  await files.getByRole("button", { name: "移動", exact: true }).click();
  await page.getByRole("dialog").getByRole("button", { name: "documents", exact: true }).click();
  await page.getByRole("dialog").getByRole("button", { name: "ここに移動" }).click();
  await expect(files.getByRole("button", { name: "guide.md", exact: true })).toHaveCount(0);
  expect(mock.calls).toContainEqual(
    expect.objectContaining({
      action: "move",
      path: "guide.md",
      destination: "documents/guide.md",
      confirm: true,
    }),
  );
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.screenshot({ path: test.info().outputPath("explorer-dark.png"), fullPage: true });
  await page.evaluate(() => document.documentElement.classList.remove("dark"));
  await page.screenshot({ path: test.info().outputPath("explorer-light.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await files.getByRole("checkbox", { name: "config.toml を選択", exact: true }).check();
  await page.screenshot({ path: test.info().outputPath("explorer-mobile.png"), fullPage: true });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  clean();
});

test("drop uploads, partial failures, binary files and oversized inputs", async ({ page }) => {
  const clean = watchConsole(page);
  const mock = await mockFiles(page);
  await page.goto(`${server.origin}/hosts/demo?tab=files`);
  const files = page.getByRole("region", { name: "ファイル管理", exact: true });
  await expect(files.getByRole("button", { name: "config.toml", exact: true })).toBeVisible();
  const dropped = await page.evaluateHandle(() => {
    const data = new DataTransfer();
    data.items.add(new File(["dropped content"], "drop.txt", { type: "text/plain" }));
    return data;
  });
  await files
    .getByRole("button", { name: "一覧を更新" })
    .dispatchEvent("drop", { dataTransfer: dropped });
  await expect(files.getByRole("button", { name: "drop.txt", exact: true })).toBeVisible();
  await files.getByLabel("ファイルをアップロード", { exact: true }).setInputFiles([
    { name: "first.txt", mimeType: "text/plain", buffer: Buffer.from("first") },
    { name: "README.md", mimeType: "text/plain", buffer: Buffer.from("collision") },
    { name: "last.txt", mimeType: "text/plain", buffer: Buffer.from("never sent") },
  ]);
  await expect(files.getByRole("alert")).toContainText("1 / 3 件");
  expect(mock.nodes.has("first.txt")).toBe(true);
  expect(mock.nodes.has("last.txt")).toBe(false);
  const before = mock.calls.length;
  await files.getByLabel("ファイルをアップロード", { exact: true }).setInputFiles({
    name: "oversize.txt",
    mimeType: "text/plain",
    buffer: Buffer.alloc(256 * 1024 + 1),
  });
  await expect(files.getByRole("alert")).toContainText("256 KiB");
  expect(mock.calls).toHaveLength(before);
  await files.getByRole("button", { name: "binary.dat", exact: true }).click();
  await expect(files.getByRole("alert")).toContainText("ダウンロードして確認");
  await expect(files.getByRole("button", { name: "ダウンロード", exact: true })).toBeEnabled();
  clean();
});

test("read-only explorer keeps browsing and editing controls reflect permissions", async ({
  page,
}) => {
  const clean = watchConsole(page);
  const mock = await mockFiles(page, true);
  await page.goto(`${server.origin}/hosts/demo?tab=files`);
  const files = page.getByRole("region", { name: "ファイル管理", exact: true });
  await files.getByRole("button", { name: "README.md", exact: true }).click();
  await expect(files.getByLabel("UTF-8テキスト")).toHaveAttribute("readonly", "");
  for (const name of [
    "アップロード",
    "新しいフォルダ",
    "新しいファイル",
    "名前変更",
    "移動",
    "削除",
    "変更を保存",
  ])
    await expect(files.getByRole("button", { name, exact: true })).toHaveCount(0);
  await expect(files.getByText("公開ディレクトリの権限設定", { exact: true })).toHaveCount(0);
  expect(mock.calls.every((q) => q.action === "read" || q.action === "list")).toBe(true);
  clean();
});
