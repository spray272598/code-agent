import * as React from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

// Email verification landing page. The backend sends a link like
// /verify?token=... that we POST back to /api/v1/auth/verify.
export default function Verify() {
  const [params] = useSearchParams();
  const nav = useNavigate();
  const [busy, setBusy] = React.useState(false);
  const [status, setStatus] = React.useState<"pending" | "ok" | "fail">("pending");
  const [err, setErr] = React.useState<string | null>(null);

  React.useEffect(() => {
    const token = params.get("token");
    if (!token) {
      setStatus("fail");
      setErr("missing token");
      return;
    }
    setBusy(true);
    api<{ userId: string }>("/api/v1/auth/verify", {
      method: "POST",
      body: JSON.stringify({ token }),
    }, { auth: false })
      .then(() => setStatus("ok"))
      .catch((e) => {
        setStatus("fail");
        setErr((e as Error).message);
      })
      .finally(() => setBusy(false));
  }, [params]);

  return (
    <div className="min-h-full flex items-center justify-center px-4">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>邮箱验证</CardTitle>
          <CardDescription>正在校验您的验证 token</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {status === "pending" && <div className="text-sm text-muted-foreground">{busy ? "校验中…" : "准备校验"}</div>}
          {status === "ok" && (
            <>
              <div className="text-emerald-400 text-sm">验证成功！</div>
              <Button onClick={() => nav("/signin")} className="w-full">前往登录</Button>
            </>
          )}
          {status === "fail" && <div className="text-destructive text-sm">{err ?? "校验失败"}</div>}
          <Link to="/signin" className="block text-center text-sm text-muted-foreground hover:text-primary">
            返回登录
          </Link>
        </CardContent>
      </Card>
    </div>
  );
}