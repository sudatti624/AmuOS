import { expect, test } from "@playwright/test";
import {
  login,
  nav,
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

const memberPassword = "member passphrase for tests";

test("invitation, permission-based display, API denial and live permission changes", async ({
  browser,
}) => {
  const ownerContext = await browser.newContext();
  const owner = await ownerContext.newPage();
  const ownerClean = watchConsole(owner);
  await login(owner, server.origin, "owner", ownerPassword);

  // Host registration slot.
  await nav(owner).getByRole("link", { name: "ホスト" }).click();
  await owner.getByRole("button", { name: "登録枠を追加" }).click();
  await owner.getByLabel("表示名").fill("web-01");
  await owner.getByRole("button", { name: "追加", exact: true }).click();
  await expect(owner.getByRole("cell", { name: "web-01" })).toBeVisible();
  await expect(owner.getByText("登録待ち（Agent未接続）")).toBeVisible();

  // Invite a Viewer for all hosts.
  await nav(owner).getByRole("link", { name: "ユーザーとグループ" }).click();
  await owner.getByRole("tab", { name: "招待" }).click();
  await owner.getByLabel("ユーザー名").fill("member");
  await owner.getByRole("button", { name: "招待を発行" }).click();
  const token = (await owner.getByTestId("invitation-token").textContent())?.trim() ?? "";
  expect(token.length).toBeGreaterThan(20);

  // The invited person creates the account in a separate browser.
  const memberContext = await browser.newContext();
  const member = await memberContext.newPage();
  const memberClean = watchConsole(member);
  await member.goto(`${server.origin}/invite`);
  await member.getByLabel("招待トークン").fill(token);
  await member.getByLabel("パスワード", { exact: true }).fill(memberPassword);
  await member.getByLabel("パスワード（確認）").fill(memberPassword);
  await member.getByRole("button", { name: "アカウントを作成" }).click();
  await expect(member.getByText("アカウント「member」を作成しました。")).toBeVisible();
  await login(member, server.origin, "member", memberPassword);

  // A Viewer sees hosts but no management or audit pages.
  await expect(nav(member).getByRole("link", { name: "ホスト" })).toBeVisible();
  await expect(nav(member).getByRole("link", { name: "ユーザーとグループ" })).toHaveCount(0);
  await expect(nav(member).getByRole("link", { name: "アクティビティ" })).toHaveCount(0);
  await nav(member).getByRole("link", { name: "ホスト" }).click();
  await expect(member.getByRole("cell", { name: "web-01" })).toBeVisible();
  await expect(member.getByRole("button", { name: "登録枠を追加" })).toHaveCount(0);
  await member.goto(`${server.origin}/users`);
  await expect(
    member.getByRole("heading", { name: "このページを表示する権限がありません" }),
  ).toBeVisible();

  // Hiding the page is not the protection: the API itself refuses.
  expect((await member.request.get(`${server.origin}/api/v1/users`)).status()).toBe(403);
  expect((await member.request.get(`${server.origin}/api/v1/audit`)).status()).toBe(403);

  // The owner grants Admin through a group.
  await owner.getByRole("tab", { name: "グループ" }).click();
  await owner.getByLabel("新しいグループ").fill("admins");
  await owner.getByRole("button", { name: "作成" }).click();
  const row = owner.getByRole("row", { name: /admins/ });
  await row.getByRole("button", { name: "ロール" }).click();
  await owner.getByRole("dialog").getByRole("button", { name: "ロールを追加" }).click();
  await owner.getByRole("dialog").getByLabel("ロール 1", { exact: true }).selectOption("Admin");
  await owner.getByRole("dialog").getByRole("button", { name: "保存" }).click();
  await expect(row.getByText("Admin（全ホスト）")).toBeVisible();
  await row.getByRole("button", { name: "メンバー" }).click();
  await owner.getByRole("dialog").getByLabel("member").check();
  await owner.getByRole("dialog").getByRole("button", { name: "保存" }).click();
  await expect(row.getByRole("cell", { name: "member" })).toBeVisible();

  // The member's existing session uses the new grants without logging in again.
  await member.goto(`${server.origin}/`);
  await expect(nav(member).getByRole("link", { name: "ユーザーとグループ" })).toBeVisible();
  expect((await member.request.get(`${server.origin}/api/v1/users`)).status()).toBe(200);

  // Removing the membership takes the access away again.
  await row.getByRole("button", { name: "メンバー" }).click();
  await owner.getByRole("dialog").getByLabel("member").uncheck();
  await owner.getByRole("dialog").getByRole("button", { name: "保存" }).click();
  await expect(row.getByRole("cell", { name: "なし" })).toBeVisible();
  expect((await member.request.get(`${server.origin}/api/v1/users`)).status()).toBe(403);

  // The owner reviews the audit log.
  await nav(owner).getByRole("link", { name: "アクティビティ" }).click();
  await expect(owner.getByRole("cell", { name: "auth.login" }).first()).toBeVisible();

  ownerClean();
  memberClean();
  await ownerContext.close();
  await memberContext.close();
});
