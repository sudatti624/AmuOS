import type { ReactNode } from "react";
import { NavLink, Navigate, Outlet } from "react-router";
import {
  ActivityIcon,
  HouseIcon,
  LogOutIcon,
  ServerIcon,
  SettingsIcon,
  UsersIcon,
} from "lucide-react";
import { useSession } from "@/app/session";
import { Loading } from "@/components/common";
import { Button } from "@/components/ui/button";
import { canManageIdentity, canReadAudit, canReadHosts } from "@/lib/permissions";
import { cn } from "@/lib/utils";

function Brand() {
  return (
    <div className="flex items-center gap-2 px-2">
      <img src="/favicon.svg" alt="" className="size-7" />
      <span className="text-base font-semibold tracking-tight">AmuOS</span>
    </div>
  );
}

export function AuthShell({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: ReactNode;
}) {
  return (
    <main className="flex min-h-dvh items-center justify-center p-4">
      <div className="flex w-full max-w-sm flex-col gap-6">
        <Brand />
        <div className="flex flex-col gap-1">
          <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
          <p className="text-sm text-muted-foreground">{description}</p>
        </div>
        {children}
      </div>
    </main>
  );
}

export function AppLayout() {
  const { me, loading, logout } = useSession();
  if (loading) return <Loading />;
  if (!me) return <Navigate to="/login" replace />;

  // Monitoring and Tasks are not offered yet, so they have no navigation entry.
  const items = [
    { to: "/", label: "概要", icon: HouseIcon, show: true },
    { to: "/hosts", label: "ホスト", icon: ServerIcon, show: canReadHosts(me.grants) },
    { to: "/activity", label: "アクティビティ", icon: ActivityIcon, show: canReadAudit(me.grants) },
    {
      to: "/users",
      label: "ユーザーとグループ",
      icon: UsersIcon,
      show: canManageIdentity(me.grants),
    },
    { to: "/settings", label: "設定", icon: SettingsIcon, show: true },
  ].filter((item) => item.show);

  const nav = (
    <ul className="flex gap-1 md:flex-col">
      {items.map(({ to, label, icon: Icon }) => (
        <li key={to}>
          <NavLink
            to={to}
            end={to === "/"}
            className={({ isActive }) =>
              cn(
                "flex items-center gap-2 rounded-md px-3 py-2 text-sm whitespace-nowrap text-muted-foreground outline-none hover:bg-accent hover:text-accent-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50",
                isActive && "bg-accent font-medium text-accent-foreground",
              )
            }
          >
            <Icon className="size-4" />
            {label}
          </NavLink>
        </li>
      ))}
    </ul>
  );

  return (
    <div className="flex min-h-dvh flex-col md:flex-row">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-50 focus:rounded-md focus:bg-card focus:px-3 focus:py-2"
      >
        本文へ移動
      </a>
      <aside className="flex flex-col gap-4 border-b bg-sidebar p-3 md:sticky md:top-0 md:h-dvh md:w-60 md:border-r md:border-b-0">
        <div className="flex items-center justify-between md:flex-col md:items-stretch md:gap-4">
          <Brand />
          <div className="flex items-center gap-2 md:hidden">
            <span className="text-sm text-muted-foreground">{me.username}</span>
            <Button
              variant="ghost"
              size="icon"
              onClick={() => void logout()}
              aria-label="ログアウト"
            >
              <LogOutIcon />
            </Button>
          </div>
        </div>
        <nav aria-label="メイン" className="overflow-x-auto">
          {nav}
        </nav>
        <div className="mt-auto hidden flex-col gap-2 border-t pt-3 md:flex">
          <p className="truncate px-2 text-sm" title={me.username}>
            <span className="text-muted-foreground">ログイン中: </span>
            <span className="font-medium">{me.username}</span>
          </p>
          <Button variant="ghost" className="justify-start" onClick={() => void logout()}>
            <LogOutIcon />
            ログアウト
          </Button>
        </div>
      </aside>
      <main id="main" className="flex-1 px-4 py-6 md:px-8">
        <div className="mx-auto flex max-w-6xl flex-col gap-6">
          <Outlet />
        </div>
      </main>
    </div>
  );
}
