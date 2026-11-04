import { expect, test } from "@playwright/test";
import {
  login,
  ownerPassword,
  startControlPlane,
  watchConsole,
  type ControlPlane,
} from "./support";

let server: ControlPlane;
test.beforeAll(async () => {
  server = await startControlPlane();
});
test.afterAll(async () => {
  await server.stop();
});

test("first setup, login, reload and logout", async ({ page }) => {
  const clean = watchConsole(page);

  // Unauthenticated visits go to the login page.
  await page.goto(`${server.origin}/hosts`);
  await expect(page).toHaveURL(/\/login$/);

  await page.getByRole("link", { name: "初回設定（最初のオーナーの作成）" }).click();
  await page.getByLabel("初回設定トークン").fill(server.setupToken);
  await page.getByLabel("ユーザー名").fill("owner");
  await page.getByLabel("パスワード", { exact: true }).fill(ownerPassword);
  await page.getByLabel("パスワード（確認）").fill(ownerPassword);
  await page.getByRole("button", { name: "オーナーを作成" }).click();
  await expect(page.getByText("オーナーを作成しました。ログインしてください。")).toBeVisible();

  // A wrong password is rejected without revealing which field was wrong.
  await page.getByLabel("ユーザー名").fill("owner");
  await page.getByLabel("パスワード").fill("not the right password");
  await page.getByRole("button", { name: "ログイン" }).click();
  await expect(page.getByText("ユーザー名またはパスワードが正しくありません。")).toBeVisible();

  await login(page, server.origin, "owner", ownerPassword);
  await expect(page.getByText("owner としてログインしています。")).toBeVisible();

  // The tab keeps its session across reloads.
  await page.reload();
  await expect(page.getByRole("heading", { name: "概要" })).toBeVisible();

  // Setup cannot be repeated once an owner exists.
  await page.goto(`${server.origin}/setup`);
  await page.getByLabel("初回設定トークン").fill(server.setupToken);
  await page.getByLabel("ユーザー名").fill("second");
  await page.getByLabel("パスワード", { exact: true }).fill(ownerPassword);
  await page.getByLabel("パスワード（確認）").fill(ownerPassword);
  await page.getByRole("button", { name: "オーナーを作成" }).click();
  await expect(page.getByRole("alert")).toBeVisible();

  await page.goto(`${server.origin}/`);
  await page.getByRole("button", { name: "ログアウト" }).click();
  await expect(page.getByText("ログアウトしました。")).toBeVisible();
  // The server session is gone, not only the local state.
  const me = await page.request.get(`${server.origin}/api/v1/me`);
  expect(me.status()).toBe(401);
  clean();
});
