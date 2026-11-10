import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";
import {
  login,
  ownerPassword,
  setupOwner,
  startControlPlane,
  watchConsole,
  type ControlPlane,
} from "./support";

let server: ControlPlane;
test.beforeAll(async () => {
  server = await startControlPlane({ files: true });
  await setupOwner(server);
});
test.afterAll(async () => {
  await server.stop();
});

test("files can be uploaded, edited, moved, downloaded and deleted through a real Agent", async ({
  page,
}) => {
  test.setTimeout(240_000);
  const clean = watchConsole(page);
  await login(page, server.origin, "owner", ownerPassword);
  const csrf = await page.evaluate(() => sessionStorage.getItem("amuos.csrf") ?? "");
  const headers = {
    Origin: server.origin,
    "X-CSRF-Token": csrf,
    "Idempotency-Key": "file-e2e-host-create-001",
  };
  const policy = await page.request.put(`${server.origin}/api/v1/monitoring/settings`, {
    headers,
    data: { sample_interval_seconds: 5, raw_retention_days: 7, rollup_retention_days: 30 },
  });
  expect(policy.ok()).toBeTruthy();
  const created = await page.request.post(`${server.origin}/api/v1/hosts`, {
    headers,
    data: { name: "file-host" },
  });
  expect(created.status()).toBe(201);
  const host = await created.json();
  const enrollment = await page.request.post(
    `${server.origin}/api/v1/hosts/${host.id}/enrollment`,
    { headers, data: { ttl_minutes: 10 } },
  );
  expect(enrollment.status()).toBe(201);
  await server.startAgent((await enrollment.json()).code);
  await page.goto(`${server.origin}/hosts/${host.id}`);
  const files = page.getByRole("region", { name: "ファイル管理" });
  await files.getByText("公開ディレクトリの権限設定", { exact: true }).click();
  await files.getByLabel("ルート名", { exact: true }).fill("work");
  await files.getByRole("button", { name: "ルートの権限を保存" }).click();
  await expect(files.getByLabel("許可ディレクトリ")).toHaveValue("work");
  await files.getByRole("button", { name: "一覧を取得" }).click();
  await expect(files.getByRole("button", { name: "一覧を取得" })).toBeEnabled({ timeout: 40_000 });
  await files.getByLabel("ファイルをアップロード", { exact: false }).setInputFiles({
    name: "hello.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("private file content"),
  });
  await expect(files.getByRole("button", { name: "hello.txt", exact: true })).toBeVisible({
    timeout: 40_000,
  });
  await server.stopAgent();
  await server.restart();
  await server.restartAgent();
  await page.reload();
  await expect(files.getByLabel("許可ディレクトリ")).toHaveValue("work");
  await files.getByRole("button", { name: "一覧を取得" }).click();
  await expect(files.getByRole("button", { name: "hello.txt", exact: true })).toBeVisible({
    timeout: 40_000,
  });
  await files.getByRole("button", { name: "hello.txt", exact: true }).click();
  await expect(files.getByRole("button", { name: "テキストを開く" })).toBeEnabled({
    timeout: 40_000,
  });
  await files.getByRole("button", { name: "テキストを開く" }).click();
  await expect(files.getByLabel("UTF-8テキスト")).toHaveValue("private file content", {
    timeout: 40_000,
  });
  await files.getByLabel("UTF-8テキスト").fill("edited private content");
  await page.screenshot({ path: test.info().outputPath("file-manager.png"), fullPage: true });
  await files.getByRole("button", { name: "変更を保存" }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "実行", exact: true }).click();
  await expect(files.getByRole("button", { name: "一覧を取得" })).toBeEnabled({ timeout: 40_000 });
  await files.getByRole("button", { name: "hello.txt", exact: true }).click();
  await expect(files.getByLabel("移動先・変更後の相対パス")).toBeEnabled({ timeout: 40_000 });
  await files.getByLabel("移動先・変更後の相対パス").fill("renamed.txt");
  await files.getByRole("button", { name: "名前変更・移動", exact: true }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "実行", exact: true }).click();
  await expect(files.getByRole("button", { name: "renamed.txt", exact: true })).toBeVisible({
    timeout: 40_000,
  });
  await files.getByRole("button", { name: "renamed.txt", exact: true }).click();
  await expect(files.getByRole("button", { name: "ダウンロード", exact: true })).toBeEnabled({
    timeout: 40_000,
  });
  const downloaded = page.waitForEvent("download", { timeout: 40_000 });
  await files.getByRole("button", { name: "ダウンロード", exact: true }).click();
  const download = await downloaded;
  expect(download.suggestedFilename()).toBe("renamed.txt");
  expect(await readFile((await download.path())!, "utf8")).toBe("edited private content");
  await files.getByRole("button", { name: "削除", exact: true }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "実行", exact: true }).click();
  await expect(files.getByRole("button", { name: "一覧を取得" })).toBeEnabled({ timeout: 40_000 });
  await expect(files.getByRole("button", { name: "renamed.txt", exact: true })).toHaveCount(0);
  const audit = await (await page.request.get(`${server.origin}/api/v1/audit`)).text();
  expect(audit).toContain("file.request");
  expect(audit).not.toContain("private file content");
  expect(audit).not.toContain("edited private content");
  clean();
});
