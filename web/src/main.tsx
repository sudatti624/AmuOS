import { StrictMode, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { setNonce } from "get-nonce";
import { BrowserRouter, Link, Route, Routes } from "react-router";
import { ConfirmProvider } from "@/app/confirm";
import { NotificationProvider } from "@/app/notifications";
import { ReauthProvider } from "@/app/reauth";
import { SessionProvider, useMe } from "@/app/session";
import { PageHeader } from "@/components/common";
import { AppLayout } from "@/components/layout";
import type { Grant } from "@/lib/api";
import { canManageIdentity, canReadAudit, canReadHosts } from "@/lib/permissions";
import { applyTheme, storedTheme } from "@/lib/theme";
import { ActivityPage } from "@/pages/activity";
import { InvitePage, LoginPage, SetupPage } from "@/pages/auth";
import { HostsPage } from "@/pages/hosts";
import { OverviewPage } from "@/pages/overview";
import { SettingsPage } from "@/pages/settings";
import { UsersPage } from "@/pages/users";
import "./index.css";

// Dialogs insert <style> elements; the per-response nonce lets the strict CSP allow them.
const nonce = document.querySelector<HTMLMetaElement>('meta[property="csp-nonce"]')?.nonce;
if (nonce) setNonce(nonce);

applyTheme(storedTheme());

// Hides pages the current grants cannot use; the API still enforces access.
function Allowed({ when, children }: { when: (grants: Grant[]) => boolean; children: ReactNode }) {
  const me = useMe();
  if (when(me.grants)) return children;
  return (
    <PageHeader
      title="このページを表示する権限がありません"
      description="必要な場合は、管理者にロールの付与を依頼してください。"
    />
  );
}

function NotFound() {
  return (
    <main className="flex min-h-dvh flex-col items-center justify-center gap-3 p-4">
      <h1 className="text-xl font-semibold">ページが見つかりません</h1>
      <Link to="/" className="text-sm text-primary underline-offset-4 hover:underline">
        概要へ戻る
      </Link>
    </main>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      <NotificationProvider>
        <SessionProvider>
          <ConfirmProvider>
            <ReauthProvider>
              <Routes>
                <Route path="/login" element={<LoginPage />} />
                <Route path="/setup" element={<SetupPage />} />
                <Route path="/invite" element={<InvitePage />} />
                <Route element={<AppLayout />}>
                  <Route index element={<OverviewPage />} />
                  <Route
                    path="/hosts"
                    element={
                      <Allowed when={canReadHosts}>
                        <HostsPage />
                      </Allowed>
                    }
                  />
                  <Route
                    path="/activity"
                    element={
                      <Allowed when={canReadAudit}>
                        <ActivityPage />
                      </Allowed>
                    }
                  />
                  <Route
                    path="/users"
                    element={
                      <Allowed when={canManageIdentity}>
                        <UsersPage />
                      </Allowed>
                    }
                  />
                  <Route path="/settings" element={<SettingsPage />} />
                </Route>
                <Route path="*" element={<NotFound />} />
              </Routes>
            </ReauthProvider>
          </ConfirmProvider>
        </SessionProvider>
      </NotificationProvider>
    </BrowserRouter>
  </StrictMode>,
);
