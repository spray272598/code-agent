import * as React from "react";
import { api } from "@/lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

// Sprint 2.1: lightweight read-only view of the user's MCP server list via
// /api/v1/mcp/servers (Sprint 1.6: per-user). Add/edit forms land in 2.5.
type Health = { name: string; transport: string; online: boolean; tools: number; last_error?: string };

export default function MCP() {
  const [rows, setRows] = React.useState<Health[] | null>(null);
  const [err, setErr] = React.useState<string | null>(null);
  React.useEffect(() => {
    api<Health[]>("/api/v1/mcp/health")
      .then(setRows)
      .catch((e) => setErr((e as Error).message));
  }, []);
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">MCP 服务</h1>
        <p className="text-sm text-muted-foreground">当前账号的 Model Context Protocol 服务（每用户隔离）</p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>已配置服务器</CardTitle>
          <CardDescription>添加 / 删除表单在 Sprint 2.5 接入</CardDescription>
        </CardHeader>
        <CardContent>
          {err && <div className="text-sm text-destructive">{err}</div>}
          {rows === null && !err && <div className="text-sm text-muted-foreground">加载中…</div>}
          {rows && rows.length === 0 && (
            <div className="text-sm text-muted-foreground">暂无 MCP 服务</div>
          )}
          {rows && rows.length > 0 && (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b text-left text-muted-foreground">
                  <th className="py-2">名称</th>
                  <th className="py-2">传输</th>
                  <th className="py-2">状态</th>
                  <th className="py-2">工具数</th>
                  <th className="py-2">错误</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((h) => (
                  <tr key={h.name} className="border-b border-border/40 last:border-0">
                    <td className="py-2 font-medium">{h.name}</td>
                    <td className="py-2">{h.transport}</td>
                    <td className="py-2">
                      <Badge tone={h.online ? "success" : "destructive"}>{h.online ? "online" : "offline"}</Badge>
                    </td>
                    <td className="py-2">{h.tools}</td>
                    <td className="py-2 text-xs text-muted-foreground">{h.last_error ?? "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}