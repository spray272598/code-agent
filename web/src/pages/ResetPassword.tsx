import * as React from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

// Sprint 2.2: consume the reset token from the emailed link and set a new password.
export default function ResetPassword() {
  const [params] = useSearchParams();
  const nav = useNavigate();
  const token = params.get("token") ?? "";

  const [password, setPassword] = React.useState("");
  const [confirm, setConfirm] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [err, setErr] = React.useState<string | null>(null);
  const [done, setDone] = React.useState(false);

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    if (password !== confirm) {
      setErr("两次输入的新密码不一致");
      setBusy(false);
      return;
    }
    if (password.length < 8) {
      setErr("新密码至少 8 个字符");
      setBusy(false);
      return;
    }
    try {
      await api<{ ok: boolean }>("/api/v1/auth/reset-password", {
        method: "POST",
        body: JSON.stringify({ token, newPassword: password }),
      }, { auth: false });
      setDone(true);
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
          <CardTitle>重置密码</CardTitle>
          <CardDescription>设置一个新的登录密码</CardDescription>
        </CardHeader>
        <CardContent>
          {done ? (
            <div className="space-y-3">
              <div className="text-emerald-400 text-sm">密码已重置！</div>
              <Button onClick={() => nav("/signin")} className="w-full">前往登录</Button>
            </div>
          ) : (
            <form onSubmit={onSubmit} className="space-y-3">
              <div className="space-y-1">
                <Input
                  type="password"
                  placeholder="新密码（至少 8 个字符）"
                  autoComplete="new-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                />
              </div>
              <div className="space-y-1">
                <Input
                  type="password"
                  placeholder="确认新密码"
                  autoComplete="new-password"
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  required
                />
              </div>
              {err && <div className="text-sm text-destructive">{err}</div>}
              <Button type="submit" disabled={busy} className="w-full">
                {busy ? "提交中…" : "重置密码"}
              </Button>
              <Link to="/signin" className="block text-center text-sm text-muted-foreground hover:text-primary">
                返回登录
              </Link>
            </form>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
