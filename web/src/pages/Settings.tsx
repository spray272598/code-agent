import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

// Sprint 2.1 placeholder. Settings page aggregates: notification, theme,
// telemetry opt-in, etc. Full UI lands in Sprint 2.5/2.6.
export default function Settings() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">设置</h1>
        <p className="text-sm text-muted-foreground">偏好与全局开关</p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>主题</CardTitle>
          <CardDescription>深色 / 浅色 / 跟随系统</CardDescription>
        </CardHeader>
        <CardContent className="text-sm text-muted-foreground">
          Sprint 2.1 默认 dark，可在 Sprint 2.5 中添加切换。
        </CardContent>
      </Card>
    </div>
  );
}