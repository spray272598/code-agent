import * as React from "react";
import { Link } from "react-router-dom";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

// Sprint 2.2: password reset request. The backend always answers success to
// avoid leaking which accounts exist; we show a generic confirmation.
export default function ForgotPassword() {
  const [email, setEmail] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [sent, setSent] = React.useState(false);
  const [err, setErr] = React.useState<string | null>(null);

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      await api<{ ok: boolean }>("/api/v1/auth/forgot-password", {
        method: "POST",
        body: JSON.stringify({ email }),
      }, { auth: false });
      setSent(true);
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
          <CardTitle>忘记密码</CardTitle>
          <CardDescription>输入账号邮箱，我们会发送重置链接</CardDescription>
        </CardHeader>
        <CardContent>
          {sent ? (
            <div className="space-y-3 text-sm">
              <div className="text-emerald-400">重置链接已发送（如果该账号存在）。</div>
              <div className="text-muted-foreground">
                请检查收件箱，使用邮件中的链接设置新密码。
              </div>
              <Link to="/signin" className="block text-center text-sm text-primary hover:underline">
                返回登录
              </Link>
            </div>
          ) : (
            <form onSubmit={onSubmit} className="space-y-3">
              <div className="space-y-1">
                <Input
                  type="email"
                  placeholder="email@example.com"
                  autoComplete="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  required
                />
              </div>
              {err && <div className="text-sm text-destructive">{err}</div>}
              <Button type="submit" disabled={busy} className="w-full">
                {busy ? "发送中…" : "发送重置链接"}
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
