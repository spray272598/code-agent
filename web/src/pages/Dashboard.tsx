import * as React from "react";
import { api } from "@/lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

// Sprint 2.1 dashboard: greets the user, shows the /me payload. Richer
// widgets (token usage, recent sessions, agent activity) land in Sprint 2.4+.
export default function Dashboard() {
  const [me, setMe] = React.useState<Record<string, unknown> | null>(null);
  React.useEffect(() => {
    api<Record<string, unknown>>("/api/v1/me")
      .then(setMe)
      .catch(() => setMe({ error: "fetch failed" }));
  }, []);
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">概览</h1>
        <p className="text-sm text-muted-foreground">当前账号会话与最近状态</p>
      </div>
      <div className="grid gap-4 md:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle>会话</CardTitle>
            <CardDescription>JWT Principal</CardDescription>
          </CardHeader>
          <CardContent className="text-sm space-y-1">
            <div>userId: <code>{String(me?.userId ?? "—")}</code></div>
            <div>role: <Badge>{String(me?.role ?? "—")}</Badge></div>
            <div>deviceId: <code className="text-xs">{String(me?.deviceId ?? "—")}</code></div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>设备</CardTitle>
            <CardDescription>RFC8628 激活</CardDescription>
          </CardHeader>
          <CardContent className="text-sm">
            通过 <code>/device/approve</code> 授权 TUI/CLI 设备激活。
            <div className="mt-2 text-xs text-muted-foreground">详见 Sprint 1.4</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>凭据</CardTitle>
            <CardDescription>KMS / SSH / LLM</CardDescription>
          </CardHeader>
          <CardContent className="text-sm">
            所有密钥均通过 KMS (AES-256-GCM) 静态加密。
            <div className="mt-2 text-xs text-muted-foreground">Sprint 2.3 / 2.8 / 2.9</div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}