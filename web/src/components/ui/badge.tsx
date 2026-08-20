import * as React from "react";
import { cn } from "@/lib/cn";

// Small inline badge (status pill). Used by DeviceList, MCP server list, etc.
export const Badge = React.forwardRef<
  HTMLSpanElement,
  React.HTMLAttributes<HTMLSpanElement> & { tone?: "default" | "success" | "warning" | "destructive" | "muted" }
>(({ className, tone = "default", ...props }, ref) => {
  const toneCls = {
    default: "bg-primary/15 text-primary",
    success: "bg-emerald-500/15 text-emerald-400",
    warning: "bg-amber-500/15 text-amber-400",
    destructive: "bg-destructive/15 text-destructive",
    muted: "bg-muted text-muted-foreground",
  }[tone];
  return (
    <span
      ref={ref}
      className={cn("inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium", toneCls, className)}
      {...props}
    />
  );
});
Badge.displayName = "Badge";