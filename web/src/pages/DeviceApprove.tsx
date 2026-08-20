import * as React from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { api, getUser } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

// Sprint 2.1 / RFC8628 device approval landing page. The TUI gives the user
// a code like "ABCD-1234"; the user pastes it here, sees their identity, and
// clicks Approve (or Deny). We require an authenticated session — the link
// from the device should already route through /signin first.
export default function DeviceApprove() {
  const [params] = useSearchParams();
  const nav = useNavigate();
  const user = getUser();
  const initialCode = (params.get("code") ?? "").toUpperCase();
  const [code, setCode] = React.useState(initialCode);
  const [busy, setBusy] = React.useState(false);
  const [done, setDone] = React.useState<"approved" | "denied" | null>(null);
  const [err, setErr] = React.useState<string | null>(null);

  if (!user) {
    // unauthenticated → bounce to /signin, then come back here
    return (
      <div className="min-h-full flex items-center justify-center px-4">
        <Card className="w-full max-w-sm">
          <CardHeader>
            <CardTitle>需要登录</CardTitle>
            <CardDescription>设备激活需要已登录的会话</CardDescription>
          </CardHeader>
          <CardContent>
            <Button onClick={() => nav("/signin", { state: { from: "/device/approve" } })} className="w-full">登录</Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  const submit = async (deny: boolean) => {
    setBusy(true);
    setErr(null);
    try {
      await api("/api/v1/device/approve", {
        method: "POST",
        body: JSON.stringify({ user_code: code, deny }),
      });
      setDone(deny ? "denied" : "approved");
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="min-h-full flex items-center justify-center px-4">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>设备激活</CardTitle>
          <CardDescription>输入设备展示的 user code 以授权登录</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="text-sm text-muted-foreground">
            当前账号：<Badge tone="muted">{user.email ?? user.userId}</Badge>
          </div>
          <Input
            placeholder="ABCD-1234"
            value={code}
            onChange={(e) => setCode(e.target.value.toUpperCase())}
            maxLength={12}
            className="text-center tracking-widest font-mono"
          />
          {err && <div className="text-sm text-destructive">{err}</div>}
          {done && (
            <div className={done === "approved" ? "text-sm text-emerald-400" : "text-sm text-amber-400"}>
              {done === "approved" ? "已批准。设备现在可以轮询获取令牌了。" : "已拒绝。设备会话不会获得令牌。"}
            </div>
          )}
          <div className="flex gap-2">
            <Button variant="destructive" disabled={busy || !code || !!done} onClick={() => submit(true)} className="flex-1">
              拒绝
            </Button>
            <Button disabled={busy || !code || !!done} onClick={() => submit(false)} className="flex-1">
              {busy ? "提交中…" : "批准"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}