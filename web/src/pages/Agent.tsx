import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

// Sprint 2.1 placeholder. Sprint 2.4 adds model selection, temperature,
// max_steps, system prompt, etc. The TUI/CLI already pass these via
// flags; the Web version uses persisted user preferences.
export default function Agent() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Agent 参数</h1>
        <p className="text-sm text-muted-foreground">默认模型、温度、并发等</p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>默认参数</CardTitle>
          <CardDescription>Sprint 2.4 接入表单</CardDescription>
        </CardHeader>
        <CardContent className="text-sm text-muted-foreground">
          将通过 /api/v1/agent/preferences 读写用户偏好。
        </CardContent>
      </Card>
    </div>
  );
}