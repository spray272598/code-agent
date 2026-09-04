import * as React from "react";
import { NavLink } from "react-router-dom";
import {
  Activity,
  Bot,
  Cpu,
  Key,
  LayoutDashboard,
  Server,
  Settings,
  TerminalSquare,
} from "lucide-react";
import { getApiKey, setApiKey } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/cn";

// Single-operator shell: left sidebar + topbar. Items map to the workspace
// pages (Devices / MCP / Audit / Agent / Settings / SSH).
const NAV = [
  { to: "/", icon: LayoutDashboard, label: "概览" },
  { to: "/devices", icon: Cpu, label: "设备" },
  { to: "/mcp", icon: Bot, label: "MCP 服务" },
  { to: "/audit", icon: Activity, label: "审计日志" },
  { to: "/agent", icon: TerminalSquare, label: "Agent 参数" },
  { to: "/settings", icon: Settings, label: "设置" },
  { to: "/ssh-terminal", icon: TerminalSquare, label: "SSH 终端" },
  { to: "/ssh-connections", icon: Server, label: "SSH 连接" },
];

function maskKey(key: string): string {
  if (key.length <= 4) return "•".repeat(key.length);
  return key.slice(0, 2) + "•".repeat(Math.max(1, key.length - 4)) + key.slice(-2);
}

export function AppShell({ children }: { children: React.ReactNode }) {
  const onEditKey = () => {
    const next = window.prompt("设置 API Key（留空恢复 dev-key 默认值）", getApiKey());
    if (next === null) return;
    setApiKey(next.trim() === "" ? "dev-key" : next.trim());
  };

  return (
    <div className="flex h-full w-full">
      {/* Sidebar */}
      <aside className="hidden md:flex w-56 shrink-0 flex-col border-r bg-card/40">
        <div className="px-6 py-5 border-b">
          <div className="text-lg font-semibold tracking-tight">Code Agent</div>
          <div className="text-xs text-muted-foreground">控制台</div>
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
          <div className="mb-2 truncate">Operator · API Key: {maskKey(getApiKey())}</div>
          <Button variant="ghost" size="sm" className="w-full justify-start" onClick={onEditKey}>
            <Key className="h-4 w-4" />
            修改 API Key
          </Button>
        </div>
      </aside>

      {/* Main */}
      <main className="flex-1 flex flex-col min-w-0">
        <header className="flex h-14 items-center justify-between border-b px-6">
          <div className="md:hidden font-semibold">Code Agent</div>
          <div className="text-sm text-muted-foreground hidden md:block">单操作员 Agent 控制台</div>
          <div className="text-xs text-muted-foreground">Agent Harness</div>
        </header>
        <div className="flex-1 overflow-auto scroll-thin p-6">{children}</div>
      </main>
    </div>
  );
}
