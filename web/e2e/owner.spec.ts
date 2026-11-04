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

test("owner re-authentication and password change end every session", async ({ page }) => {
  const clean = watchConsole(page);
  await login(page, server.origin, "owner", ownerPassword);

  // Changing audit retention requires a recent re-authentication.
  await page.goto(`${server.origin}/settings`);
  await page.getByLabel("保持期間（日）").fill("30");
  await page.getByRole("button", { name: "保存" }).click();
  const dialog = page.getByRole("dialog", { name: "パスワードの再確認" });
  await expect(dialog).toBeVisible();
  await dialog.getByLabel("パスワード").fill(ownerPassword);
  await dialog.getByRole("button", { name: "確認して続ける" }).click();
  await expect(page.getByText("監査ログの保持設定を更新しました。")).toBeVisible();
  await page.reload();
  await expect(page.getByLabel("保持期間（日）")).toHaveValue("30");

  // Changing the password ends this and every other session.
  const newPassword = "a brand new owner passphrase";
  await page.getByLabel("現在のパスワード").fill(ownerPassword);
  await page.getByLabel("新しいパスワード", { exact: true }).fill(newPassword);
  await page.getByLabel("新しいパスワード（確認）").fill(newPassword);
  await page.getByRole("button", { name: "変更する" }).click();
  await expect(page.getByText("パスワードを変更しました。")).toBeVisible();
  expect((await page.request.get(`${server.origin}/api/v1/me`)).status()).toBe(401);
  await login(page, server.origin, "owner", newPassword);
  clean();
});
