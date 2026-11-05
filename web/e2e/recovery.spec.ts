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

test("stopped encrypted recovery preserves hosts and rejects old browser sessions", async ({
  page,
}) => {
  const clean = watchConsole(page);
  await login(page, server.origin, "owner", ownerPassword);
  await page.goto(`${server.origin}/hosts`);
  await page.getByRole("button", { name: "登録枠を追加" }).click();
  await page.getByLabel("表示名").fill("recovered-host");
  await page.getByRole("button", { name: "追加", exact: true }).click();
  await expect(page.getByRole("cell", { name: "recovered-host", exact: true })).toBeVisible();

  await server.backupAndRestore();
  expect((await page.request.get(`${server.origin}/api/v1/me`)).status()).toBe(401);
  await page.reload();
  await expect(page).toHaveURL(/\/login$/);
  await login(page, server.origin, "owner", ownerPassword);
  await page.goto(`${server.origin}/hosts`);
  await expect(page.getByRole("cell", { name: "recovered-host", exact: true })).toBeVisible();
  const audit = await page.request.get(`${server.origin}/api/v1/audit`);
  expect(audit.status()).toBe(200);
  expect(await audit.text()).toContain("restore.complete");
  clean();
});
