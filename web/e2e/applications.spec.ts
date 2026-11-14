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

test("application registry persists, rejects stale updates and filters app permissions", async ({
  page,
  browser,
}) => {
  const clean = watchConsole(page);
  await login(page, server.origin, "owner", ownerPassword);
  const csrf = await page.evaluate(() => sessionStorage.getItem("amuos.csrf") ?? "");
  const headers = {
    Origin: server.origin,
    "X-CSRF-Token": csrf,
    "Idempotency-Key": "application-e2e-host-001",
  };
  const created = await page.request.post(`${server.origin}/api/v1/hosts`, {
    headers,
    data: { name: "application-host" },
  });
  expect(created.status()).toBe(201);
  const host = await created.json();
  const base = `${server.origin}/api/v1/hosts/${host.id}/applications`;
  await page.goto(`${server.origin}/hosts/${host.id}`);
  await page.getByRole("tab", { name: "アプリ", exact: true }).click();
  const apps = page.getByRole("region", { name: "アプリ登録", exact: true });
  await apps.getByLabel("アプリ名（英小文字で開始）").fill("example");
  await apps.getByLabel("Viewerに登録情報の閲覧を許可").check();
  await apps.getByRole("button", { name: "アプリ登録を保存", exact: true }).click();
  await expect(apps.getByRole("status")).toHaveText("アプリ登録を保存しました。");
  await expect(apps.getByRole("cell", { name: "example", exact: true })).toBeVisible();
  const initial = await (await page.request.get(`${base}/example`)).json();
  expect(initial.revision).toBe(1);
  await apps.getByLabel("予定するランタイム").selectOption("node");
  await apps.getByRole("button", { name: "アプリ登録を保存", exact: true }).click();
  await expect(apps.getByRole("cell", { name: "node", exact: true })).toBeVisible();
  await apps.screenshot({ path: "test-results/application-registry.png" });
  expect(
    (
      await page.request.put(`${base}/example`, {
        headers,
        data: { expected_revision: 1, config: initial.config },
      })
    ).status(),
  ).toBe(409);

  await server.restart();
  await page.reload();
  await expect(apps.getByRole("cell", { name: "node", exact: true })).toBeVisible();
  // A Viewer sees only registrations whose policy permits the same host grant.
  const invitation = await page.request.post(`${server.origin}/api/v1/invitations`, {
    headers,
    data: {
      username: "app-viewer",
      grants: [{ role: "Viewer", all_hosts: false, host_id: host.id }],
    },
  });
  expect(invitation.status()).toBe(201);
  const member = await browser.newContext({ ignoreHTTPSErrors: true });
  const viewer = await member.newPage();
  const viewerClean = watchConsole(viewer);
  const accepted = await viewer.request.post(`${server.origin}/api/v1/invitations/accept`, {
    headers: { Origin: server.origin },
    data: { token: (await invitation.json()).token, password: "application viewer passphrase" },
  });
  expect(accepted.status()).toBe(201);
  await login(viewer, server.origin, "app-viewer", "application viewer passphrase");
  await viewer.goto(`${server.origin}/hosts/${host.id}?tab=apps`);
  const visible = viewer.getByRole("region", { name: "アプリ登録", exact: true });
  await expect(visible.getByRole("cell", { name: "example", exact: true })).toBeVisible();
  await expect(visible.getByRole("button", { name: "設定", exact: true })).toHaveCount(0);
  const viewerCsrf = await viewer.evaluate(() => sessionStorage.getItem("amuos.csrf") ?? "");
  expect(
    (
      await viewer.request.put(`${base}/example`, {
        headers: { Origin: server.origin, "X-CSRF-Token": viewerCsrf },
        data: { expected_revision: 2, config: initial.config },
      })
    ).status(),
  ).toBe(403);
  const row = apps.getByRole("row", { name: /example/ });
  await row.getByRole("button", { name: "設定", exact: true }).click();
  await apps.getByLabel("Viewerに登録情報の閲覧を許可").uncheck();
  await apps.getByRole("button", { name: "アプリ登録を保存", exact: true }).click();
  await expect(apps.getByRole("status")).toHaveText("アプリ登録を保存しました。");
  expect((await viewer.request.get(`${base}/example`)).status()).toBe(404);
  await visible.getByRole("button", { name: "一覧を更新" }).click();
  await expect(visible.getByRole("cell", { name: "example", exact: true })).toHaveCount(0);
  await row.getByRole("button", { name: "登録解除", exact: true }).click();
  await page
    .getByRole("alertdialog")
    .getByRole("button", { name: "登録解除", exact: true })
    .click();
  await expect(apps.getByRole("status")).toHaveText("アプリ登録を解除しました。");
  await expect(apps.getByRole("cell", { name: "example", exact: true })).toHaveCount(0);
  expect(
    (
      await page.request.put(`${base}/example`, {
        headers,
        data: { expected_revision: 0, config: initial.config },
      })
    ).status(),
  ).toBe(409);
  clean();
  viewerClean();
  await member.close();
});
