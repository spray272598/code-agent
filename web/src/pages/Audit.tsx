import * as React from "react";
import { api } from "@/lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

// Sprint 2.1: minimal audit viewer. Sprint 2.7 adds date range / filters.
type Entry = { UserID: string; SessionID: string; Action: string; Tool: string; Detail: string; Decision: string };

export default function Audit() {
  const [rows, setRows] = React.useState<Entry[] | null>(null);
  const [err, setErr] = React.useState<string | null>(null);
  React.useEffect(() => {
    api<Entry[]>("/api/v1/audit")
      .then(setRows)
      .catch((e) => setErr((e as Error).message));
  }, []);
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">审计日志</h1>
        <p className="text-sm text-muted-foreground">仅显示当前用户的操作（Sprint 1.7 多租户隔离）</p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>最近事件</CardTitle>
          <CardDescription>最多 100 条，按时间倒序</CardDescription>
        </CardHeader>
        <CardContent>
          {err && <div className="text-sm text-destructive">{err}</div>}
          {rows === null && !err && <div className="text-sm text-muted-foreground">加载中…</div>}
          {rows && rows.length === 0 && <div className="text-sm text-muted-foreground">暂无事件</div>}
          {rows && rows.length > 0 && (
            <ul className="text-sm divide-y divide-border/40">
              {rows.map((e, i) => (
                <li key={i} className="py-2 flex items-center gap-3">
                  <Badge tone={e.Decision === "deny" ? "destructive" : "muted"}>{e.Action}</Badge>
                  <span className="text-xs text-muted-foreground">{e.Tool || "—"}</span>
                  <span className="truncate flex-1">{e.Detail}</span>
                  <span className="font-mono text-xs">{e.SessionID?.slice(0, 8) ?? "—"}</span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}