import * as React from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { api, setTokens, type AuthUser, type TokenPair } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export default function SignIn() {
  const nav = useNavigate();
  const loc = useLocation() as { state?: { from?: string } };
  const [orgSlug, setOrgSlug] = React.useState("");
  const [email, setEmail] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [err, setErr] = React.useState<string | null>(null);

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      const data = await api<TokenPair & { user: AuthUser }>("/api/v1/auth/login", {
        method: "POST",
        body: JSON.stringify({ orgSlug, email, password }),
      }, { auth: false });
      setTokens(data, data.user);
      nav(loc.state?.from ?? "/", { replace: true });
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
          <CardTitle>登录</CardTitle>
          <CardDescription>使用注册时的邮箱和密码进入控制台</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={onSubmit} className="space-y-3">
            <div className="space-y-1">
              <Input
                placeholder="组织 slug"
                autoComplete="organization"
                value={orgSlug}
                onChange={(e) => setOrgSlug(e.target.value)}
                required
              />
            </div>
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
            <div className="space-y-1">
              <Input
                type="password"
                placeholder="密码"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>
            {err && <div className="text-sm text-destructive">{err}</div>}
            <Button type="submit" disabled={busy} className="w-full">
              {busy ? "登录中…" : "登录"}
            </Button>
          </form>
          <div className="mt-4 text-sm text-muted-foreground">
            还没有账号？{" "}
            <Link to="/signup" className="text-primary hover:underline">
              注册
            </Link>
            {" · "}
            <Link to="/forgot" className="text-muted-foreground hover:text-primary hover:underline">
              忘记密码
            </Link>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}