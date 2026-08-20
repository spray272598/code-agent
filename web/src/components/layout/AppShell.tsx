import * as React from "react";
import { NavLink, useNavigate } from "react-router-dom";
import {
  Activity,
  Bot,
  Cpu,
  LayoutDashboard,
  LogOut,
  Settings,
  ShieldCheck,
  TerminalSquare,
  User,
} from "lucide-react";
import { clearTokens, getUser } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/cn";

// Sprint 2.1 shell: left sidebar + topbar. Items align with the pages we'll
// build in Sprint 2.2/2.5/2.6/2.7/2.4 (Account / Devices / MCP / Audit / Agent).
const NAV = [
  { to: "/", icon: LayoutDashboard, label: "概览" },
  { to: "/account", icon: User, label: "账号" },
  { to: "/devices", icon: Cpu, label: "设备" },
  { to: "/mcp", icon: Bot, label: "MCP 服务" },
  { to: "/llm-keys", icon: ShieldCheck, label: "LLM 凭据" },
  { to: "/audit", icon: Activity, label: "审计日志" },
  { to: "/agent", icon: TerminalSquare, label: "Agent 参数" },
  { to: "/settings", icon: Settings, label: "设置" },
];

export function AppShell({ children }: { children: React.ReactNode }) {
  const nav = useNavigate();
  const user = getUser();

  const onLogout = () => {
    clearTokens();
    nav("/signin", { replace: true });
  };

  return (
    <div className="flex h-full w-full">
      {/* Sidebar */}
      <aside className="hidden md:flex w-56 shrink-0 flex-col border-r bg-card/40">
        <div className="px-6 py-5 border-b">
          <div className="text-lg font-semibold tracking-tight">Code Agent</div>
          <div className="text-xs text-muted-foreground">控制台 · 多租户</div>
        </div>
        <nav className="flex-1 px-3 py-3 space-y-0.5">
          {NAV.map((it) => (
            <NavLink
              key={it.to}
              to={it.to}
              end={it.to === "/"}
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-2 rounded-md px-3 py-2 text-sm transition-colors",
                  isActive
                    ? "bg-primary/15 text-primary"
                    : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
                )
              }
            >
              <it.icon className="h-4 w-4" />
              <span>{it.label}</span>
            </NavLink>
          ))}
        </nav>
        <div className="border-t px-3 py-3 text-xs text-muted-foreground">
          <div className="mb-2 truncate">uid: {user?.userId ?? "?"}</div>
          <Button variant="ghost" size="sm" className="w-full justify-start" onClick={onLogout}>
            <LogOut className="h-4 w-4" />
            退出登录
          </Button>
        </div>
      </aside>

      {/* Main */}
      <main className="flex-1 flex flex-col min-w-0">
        <header className="flex h-14 items-center justify-between border-b px-6">
          <div className="md:hidden font-semibold">Code Agent</div>
          <div className="text-sm text-muted-foreground hidden md:block">
            {user?.role === "owner" ? "组织所有者" : "成员"} · {user?.email ?? user?.userId}
          </div>
          <div className="text-xs text-muted-foreground">Sprint 2.1</div>
        </header>
        <div className="flex-1 overflow-auto scroll-thin p-6">{children}</div>
      </main>
    </div>
  );
}