import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

// Sprint 2.1 placeholder. The CRUD UI (provider + alias + api_key + api_base)
// lands in Sprint 2.5 alongside the MCP form; sharing a form pattern.
export default function LLMKeys() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">LLM 凭据</h1>
        <p className="text-sm text-muted-foreground">多租户 LLM API Key（每用户 AES-256-GCM 静态加密）</p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>API Key 列表</CardTitle>
          <CardDescription>Sprint 2.5 接入表单</CardDescription>
        </CardHeader>
        <CardContent className="text-sm text-muted-foreground">
          API key/api_base 通过 KMS (AES-256-GCM) 加密后存储，运行时解密使用。
          <div className="mt-2 text-xs">列出接口会脱敏返回 api_key，仅展示前 8 字符 + …</div>
        </CardContent>
      </Card>
    </div>
  );
}