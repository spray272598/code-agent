import * as React from "react";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

interface Me {
  userId: string;
  orgId: string;
  deviceId?: string;
  email?: string;
  role: string;
  displayName?: string;
  emailVerified?: boolean;
  status?: string;
  createdAt?: string;
}

// Sprint 2.2: profile overview + display name edit + password change.
export default function Account() {
  const [me, setMe] = React.useState<Me | null>(null);
  const [loadErr, setLoadErr] = React.useState<string | null>(null);

  const [displayName, setDisplayName] = React.useState("");
  const [savingName, setSavingName] = React.useState(false);
  const [nameMsg, setNameMsg] = React.useState<{ ok: boolean; text: string } | null>(null);

  const [oldPassword, setOldPassword] = React.useState("");
  const [newPassword, setNewPassword] = React.useState("");
  const [confirmPassword, setConfirmPassword] = React.useState("");
  const [savingPw, setSavingPw] = React.useState(false);
  const [pwMsg, setPwMsg] = React.useState<{ ok: boolean; text: string } | null>(null);

  const load = React.useCallback(() => {
    api<Me>("/api/v1/me")
      .then((m) => {
        setMe(m);
        setDisplayName(m.displayName ?? "");
      })
      .catch((e) => setLoadErr((e as Error).message));
  }, []);
  React.useEffect(load, [load]);

  const saveName = async (e: React.FormEvent) => {
    e.preventDefault();
    setSavingName(true);
    setNameMsg(null);
    try {
      const m = await api<Me>("/api/v1/me/profile", {
        method: "POST",
        body: JSON.stringify({ displayName }),
      });
      setMe(m);
      setNameMsg({ ok: true, text: "显示名已更新" });
    } catch (err) {
      setNameMsg({ ok: false, text: (err as Error).message });
    } finally {
      setSavingName(false);
    }
  };

  const changePassword = async (e: React.FormEvent) => {
    e.preventDefault();
    setPwMsg(null);
    if (newPassword !== confirmPassword) {
      setPwMsg({ ok: false, text: "两次输入的新密码不一致" });
      return;
    }
    if (newPassword.length < 8) {
      setPwMsg({ ok: false, text: "新密码至少 8 个字符" });
      return;
    }
    setSavingPw(true);
    try {
      await api<{ ok: boolean }>("/api/v1/me/password", {
        method: "POST",
        body: JSON.stringify({ oldPassword, newPassword }),
      });
      setPwMsg({ ok: true, text: "密码已修改" });
      setOldPassword("");
      setNewPassword("");
      setConfirmPassword("");
    } catch (err) {
      setPwMsg({ ok: false, text: (err as Error).message });
    } finally {
      setSavingPw(false);
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">账号</h1>
        <p className="text-sm text-muted-foreground">个人信息与会话</p>
      </div>

      {loadErr && <div className="text-sm text-destructive">{loadErr}</div>}

      <Card>
        <CardHeader>
          <CardTitle>基本信息</CardTitle>
          <CardDescription>账号身份与验证状态</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          <Row label="用户 ID" value={String(me?.userId ?? "—")} mono />
          <Row label="组织 ID" value={String(me?.orgId ?? "—")} mono />
          <Row label="邮箱" value={String(me?.email ?? "—")} />
          <Row
            label="邮箱验证"
            value={
              me?.emailVerified ? (
                <Badge tone="success">已验证</Badge>
              ) : (
                <Badge tone="muted">未验证</Badge>
              )
            }
          />
          <Row label="状态" value={String(me?.status ?? "—")} />
          <Row label="角色" value={<Badge>{String(me?.role ?? "—")}</Badge>} />
          <Row label="设备 ID" value={String(me?.deviceId ?? "—")} mono />
          {me?.createdAt && <Row label="注册时间" value={new Date(me.createdAt).toLocaleString()} />}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>编辑资料</CardTitle>
          <CardDescription>更新您的显示名称</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={saveName} className="space-y-3">
            <div className="space-y-1">
              <Input
                placeholder="显示名称"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                required
              />
            </div>
            {nameMsg && (
              <div className={`text-sm ${nameMsg.ok ? "text-emerald-400" : "text-destructive"}`}>
                {nameMsg.text}
              </div>
            )}
            <Button type="submit" disabled={savingName}>
              {savingName ? "保存中…" : "保存显示名"}
            </Button>
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>修改密码</CardTitle>
          <CardDescription>输入当前密码并设置新密码（至少 8 个字符）</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={changePassword} className="space-y-3">
            <div className="space-y-1">
              <Input
                type="password"
                placeholder="当前密码"
                autoComplete="current-password"
                value={oldPassword}
                onChange={(e) => setOldPassword(e.target.value)}
                required
              />
            </div>
            <div className="space-y-1">
              <Input
                type="password"
                placeholder="新密码"
                autoComplete="new-password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                required
              />
            </div>
            <div className="space-y-1">
              <Input
                type="password"
                placeholder="确认新密码"
                autoComplete="new-password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                required
              />
            </div>
            {pwMsg && (
              <div className={`text-sm ${pwMsg.ok ? "text-emerald-400" : "text-destructive"}`}>
                {pwMsg.text}
              </div>
            )}
            <Button type="submit" disabled={savingPw}>
              {savingPw ? "提交中…" : "修改密码"}
            </Button>
          </form>
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
