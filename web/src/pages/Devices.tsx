import * as React from "react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

// Sprint 2.1 placeholder for the device list page. Sprint 2.6 will add the
// list + revoke flow. The Activate button goes to /device/approve.
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";

export default function Devices() {
  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">设备</h1>
          <p className="text-sm text-muted-foreground">RFC8628 激活 + 已登录设备</p>
        </div>
        <Link to="/device/approve">
          <Button>激活设备</Button>
        </Link>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>已激活设备</CardTitle>
          <CardDescription>Sprint 2.6 实现完整列表与吊销</CardDescription>
        </CardHeader>
        <CardContent className="text-sm text-muted-foreground">
          暂无设备。在 TUI/CLI 上执行激活流程后这里会显示条目（device id、激活时间、最后心跳）。
        </CardContent>
      </Card>
    </div>
  );
}