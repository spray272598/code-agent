import * as React from "react";
import { Link, useNavigate } from "react-router-dom";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export default function SignUp() {
  const nav = useNavigate();
  const [email, setEmail] = React.useState("");
  const [displayName, setDisplayName] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [err, setErr] = React.useState<string | null>(null);
  const [ok, setOk] = React.useState<string | null>(null);

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    setOk(null);
    try {
      await api<{ userId: string; email: string; status: string }>(
        "/api/v1/auth/signup",
        {
          method: "POST",
          body: JSON.stringify({
            email,
            displayName: displayName || email.split("@")[0],
            password,
          }),
        },
        { auth: false },
      );
      setOk("注册成功，请前往邮箱点击验证链接");
      setTimeout(() => nav("/signin"), 2000);
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
          <CardTitle>创建账号</CardTitle>
          <CardDescription>用邮箱注册一个账号即可使用</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={onSubmit} className="space-y-3">
            <Input type="email" placeholder="邮箱" value={email} onChange={(e) => setEmail(e.target.value)} required />
            <Input placeholder="显示名称（可选）" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
            <Input type="password" placeholder="密码" value={password} onChange={(e) => setPassword(e.target.value)} required />
            {err && <div className="text-sm text-destructive">{err}</div>}
            {ok && <div className="text-sm text-emerald-400">{ok}</div>}
            <Button type="submit" disabled={busy} className="w-full">
              {busy ? "提交中…" : "注册"}
            </Button>
          </form>
          <div className="mt-4 text-sm text-muted-foreground">
            已有账号？{" "}
            <Link to="/signin" className="text-primary hover:underline">登录</Link>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}