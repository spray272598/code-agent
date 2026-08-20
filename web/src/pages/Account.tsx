import * as React from "react";
import { api } from "@/lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

// Sprint 2.1 skeleton for the account/profile view. Full password change,
// display name edit, and notification settings land in Sprint 2.2.
export default function Account() {
  const [me, setMe] = React.useState<Record<string, unknown> | null>(null);
  React.useEffect(() => {
    api<Record<string, unknown>>("/api/v1/me").then(setMe).catch(() => setMe({ error: "fetch failed" }));
  }, []);
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">账号</h1>
        <p className="text-sm text-muted-foreground">个人信息与会话</p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>基本信息</CardTitle>
          <CardDescription>从 JWT 主体读取</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          <Row label="用户 ID" value={String(me?.userId ?? "—")} />
          <Row label="组织 ID" value={String(me?.orgId ?? "—")} />
          <Row label="邮箱" value={String(me?.email ?? "—")} />
          <Row label="角色" value={<Badge>{String(me?.role ?? "—")}</Badge>} />
          <Row label="设备 ID" value={String(me?.deviceId ?? "—")} mono />
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>编辑资料</CardTitle>
          <CardDescription>在 Sprint 2.2 中实现</CardDescription>
        </CardHeader>
        <CardContent className="text-sm text-muted-foreground">
          后续会添加：修改显示名、修改密码、邮箱换绑、双因素认证等。
        </CardContent>
      </Card>
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="flex justify-between border-b border-border/40 py-1.5 last:border-0">
      <span className="text-muted-foreground">{label}</span>
      <span className={mono ? "font-mono text-xs" : ""}>{value}</span>
    </div>
  );
}