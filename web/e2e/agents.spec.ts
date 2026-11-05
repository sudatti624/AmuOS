import { expect, test } from "@playwright/test";
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
  server = await startControlPlane();
  await setupOwner(server);
});
test.afterAll(async () => {
  await server.stop();
});

test("a real Agent enrolls with a one-time code, connects over mTLS and is revoked", async ({
  page,
}) => {
  const clean = watchConsole(page);
  await login(page, server.origin, "owner", ownerPassword);
  await page.goto(`${server.origin}/hosts`);
  await page.getByRole("button", { name: "登録枠を追加" }).click();
  await page.getByLabel("表示名").fill("agent-host");
  await page.getByRole("button", { name: "追加", exact: true }).click();
  const row = page.getByRole("row", { name: /agent-host/ });
  await expect(row.getByText("登録待ち")).toBeVisible();

  await row.getByRole("button", { name: "登録コード" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "発行する" }).click();
  const code = (await dialog.getByTestId("enrollment-code").textContent())?.trim() ?? "";
  expect(code).toMatch(/^[\w-]+\.[\w-]+$/);
  await dialog.getByRole("button", { name: "完了" }).click();

  await server.startAgent(code);
  await expect(async () => {
    await page.reload();
    await expect(row.getByText("接続中")).toBeVisible({ timeout: 1000 });
  }).toPass({ timeout: 20_000 });
  // The code was consumed; the host no longer offers a new one.
  await expect(row.getByRole("button", { name: "登録コード" })).toHaveCount(0);

  await row.getByRole("link", { name: "agent-host" }).click();
  await expect(page.getByRole("heading", { name: "agent-host", level: 1 })).toBeVisible();
  await expect(page.getByRole("heading", { name: "履歴" })).toBeVisible();
  if (process.platform === "linux") {
    // The Agent reads /proc on Linux; values and charts follow its first sample.
    await expect(page.getByText("ホスト情報")).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText("CPU使用率").first()).toBeVisible();
    await expect(page.getByRole("img", { name: "メモリ・Swap使用率のグラフ" })).toBeVisible({
      timeout: 20_000,
    });
  } else {
    await expect(page.getByText("まだ監視値を受信していません。")).toBeVisible();
  }
  await page.getByRole("tab", { name: "30日" }).click();
  await expect(page.getByRole("tab", { name: "30日" })).toHaveAttribute("aria-selected", "true");
  await page.getByRole("link", { name: "ホスト一覧" }).click();

  await row.getByRole("button", { name: "失効" }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "失効する" }).click();
  await expect(row.getByText("失効", { exact: true })).toBeVisible();
  await server.stopAgent();

  const audit = await (await page.request.get(`${server.origin}/api/v1/audit`)).text();
  expect(audit).toContain("agent.enroll");
  expect(audit).toContain("host.revoke");
  expect(audit).not.toContain(code.split(".")[1]);
  clean();
});
