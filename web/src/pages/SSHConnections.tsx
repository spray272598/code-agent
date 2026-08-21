import * as React from "react";
import { api } from "@/lib/api";

interface SSHConnection {
  ID: string;
  Name: string;
  Host: string;
  Port: number;
  Username: string;
  AuthType: string;
  Enabled: boolean;
  LastConnectedAt: string;
}

const emptyForm = {
  Name: "",
  Host: "",
  Port: 22,
  Username: "",
  AuthType: "password",
  Password: "",
  PrivateKey: "",
  Enabled: true,
};

export default function SSHConnections() {
  const [list, setList] = React.useState<SSHConnection[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [form, setForm] = React.useState({ ...emptyForm });
  const [saving, setSaving] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const refresh = React.useCallback(() => {
    setLoading(true);
    api<SSHConnection[]>("/api/v1/ssh/connections")
      .then(setList)
      .catch((e) => setError(String(e?.message ?? e)))
      .finally(() => setLoading(false));
  }, []);

  React.useEffect(refresh, [refresh]);

  const save = async () => {
    if (!form.Name || !form.Host || !form.Username) {
      setError("名称、主机、用户名均为必填");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await api("/api/v1/ssh/connections", {
        method: "POST",
        body: JSON.stringify(form),
      });
      setForm({ ...emptyForm });
      refresh();
    } catch (e: any) {
      setError(String(e?.message ?? e));
    } finally {
      setSaving(false);
    }
  };

  const remove = async (name: string) => {
    if (!window.confirm(`确认删除 SSH 连接「${name}」？`)) return;
    try {
      await api(`/api/v1/ssh/connections/${encodeURIComponent(name)}`, {
        method: "DELETE",
      });
      refresh();
    } catch (e: any) {
      setError(String(e?.message ?? e));
    }
  };

  return (
    <div className="space-y-6">
      <div className="grid gap-6 lg:grid-cols-2">
        <section className="space-y-3 rounded-lg border bg-card p-4">
          <h2 className="text-sm font-semibold">SSH 连接列表</h2>
          {loading ? (
            <p className="text-sm text-muted-foreground">加载中…</p>
          ) : list.length === 0 ? (
            <p className="text-sm text-muted-foreground">暂无连接，请在右侧添加。</p>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-muted-foreground">
                  <th className="py-1">名称</th>
                  <th>主机</th>
                  <th>用户</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {list.map((c) => (
                  <tr key={c.ID || c.Name} className="border-t">
                    <td className="py-1 font-medium">{c.Name}</td>
                    <td className="text-muted-foreground">
                      {c.Username}@{c.Host}:{c.Port}
                    </td>
                    <td className="text-muted-foreground">{c.AuthType}</td>
                    <td className="text-right">
                      <button
                        onClick={() => remove(c.Name)}
                        className="text-xs text-red-500 hover:underline"
                      >
                        删除
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>

        <section className="space-y-3 rounded-lg border bg-card p-4">
          <h2 className="text-sm font-semibold">添加 SSH 连接</h2>
          <div className="grid grid-cols-2 gap-3">
            <Field label="名称">
              <input
                className="w-full rounded-md border bg-card px-3 py-2 text-sm"
                value={form.Name}
                onChange={(e) => setForm({ ...form, Name: e.target.value })}
              />
            </Field>
            <Field label="主机">
              <input
                className="w-full rounded-md border bg-card px-3 py-2 text-sm"
                value={form.Host}
                onChange={(e) => setForm({ ...form, Host: e.target.value })}
              />
            </Field>
            <Field label="端口">
              <input
                type="number"
                className="w-full rounded-md border bg-card px-3 py-2 text-sm"
                value={form.Port}
                onChange={(e) => setForm({ ...form, Port: Number(e.target.value) })}
              />
            </Field>
            <Field label="用户名">
              <input
                className="w-full rounded-md border bg-card px-3 py-2 text-sm"
                value={form.Username}
                onChange={(e) => setForm({ ...form, Username: e.target.value })}
              />
            </Field>
          </div>
          <Field label="认证方式">
            <select
              className="w-full rounded-md border bg-card px-3 py-2 text-sm"
              value={form.AuthType}
              onChange={(e) => setForm({ ...form, AuthType: e.target.value })}
            >
              <option value="password">密码</option>
              <option value="private_key">私钥</option>
            </select>
          </Field>
          {form.AuthType === "password" ? (
            <Field label="密码">
              <input
                type="password"
                className="w-full rounded-md border bg-card px-3 py-2 text-sm"
                value={form.Password}
                onChange={(e) => setForm({ ...form, Password: e.target.value })}
              />
            </Field>
          ) : (
            <Field label="私钥">
              <textarea
                className="w-full rounded-md border bg-card px-3 py-2 text-sm"
                rows={4}
                value={form.PrivateKey}
                onChange={(e) => setForm({ ...form, PrivateKey: e.target.value })}
              />
            </Field>
          )}
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={form.Enabled}
              onChange={(e) => setForm({ ...form, Enabled: e.target.checked })}
            />
            启用
          </label>
          {error && <p className="text-xs text-red-500">{error}</p>}
          <button
            onClick={save}
            disabled={saving}
            className="rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground disabled:opacity-50"
          >
            {saving ? "保存中…" : "保存连接"}
          </button>
        </section>
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block space-y-1">
      <span className="text-xs text-muted-foreground">{label}</span>
      {children}
    </label>
  );
}
