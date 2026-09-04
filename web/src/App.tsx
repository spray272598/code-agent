import * as React from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { AppShell } from "@/components/layout/AppShell";

import Dashboard from "@/pages/Dashboard";
import Devices from "@/pages/Devices";
import MCP from "@/pages/MCP";
import Audit from "@/pages/Audit";
import Agent from "@/pages/Agent";
import Settings from "@/pages/Settings";
import SSHTerminal from "@/pages/SSHTerminal";
import SSHConnections from "@/pages/SSHConnections";

// The harness is single-operator; there is no sign-in flow. All pages live behind
// the AppShell and authenticate via the static API key (X-API-Key header).
function NotFound() {
  return (
    <div className="min-h-full flex items-center justify-center px-4">
      <div className="text-center">
        <div className="text-3xl font-bold">404</div>
        <div className="text-sm text-muted-foreground mt-2">页面不存在</div>
      </div>
    </div>
  );
}

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<AppShell><Dashboard /></AppShell>} />
        <Route path="/devices" element={<AppShell><Devices /></AppShell>} />
        <Route path="/mcp" element={<AppShell><MCP /></AppShell>} />
        <Route path="/audit" element={<AppShell><Audit /></AppShell>} />
        <Route path="/agent" element={<AppShell><Agent /></AppShell>} />
        <Route path="/settings" element={<AppShell><Settings /></AppShell>} />
        <Route path="/ssh-terminal" element={<AppShell><SSHTerminal /></AppShell>} />
        <Route path="/ssh-connections" element={<AppShell><SSHConnections /></AppShell>} />

        <Route path="/404" element={<NotFound />} />
        <Route path="*" element={<Navigate to="/404" replace />} />
      </Routes>
    </BrowserRouter>
  );
}
