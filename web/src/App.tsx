import * as React from "react";
import { BrowserRouter, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { AppShell } from "@/components/layout/AppShell";
import { RequireAuth } from "@/components/RequireAuth";
import { bootstrapAuth } from "@/lib/api";

import SignIn from "@/pages/SignIn";
import SignUp from "@/pages/SignUp";
import Verify from "@/pages/Verify";
import ForgotPassword from "@/pages/ForgotPassword";
import ResetPassword from "@/pages/ResetPassword";
import DeviceApprove from "@/pages/DeviceApprove";
import Dashboard from "@/pages/Dashboard";
import Account from "@/pages/Account";
import Devices from "@/pages/Devices";
import MCP from "@/pages/MCP";
import LLMKeys from "@/pages/LLMKeys";
import Audit from "@/pages/Audit";
import Agent from "@/pages/Agent";
import Settings from "@/pages/Settings";
import SSHTerminal from "@/pages/SSHTerminal";
import SSHConnections from "@/pages/SSHConnections";

// Bootstrap the auth store from localStorage before rendering. We also
// bounce to /signin on the global "auth-expired" event fired by the API
// client when refresh fails.
function AuthBridge({ children }: { children: React.ReactNode }) {
  const loc = useLocation();
  React.useEffect(() => {
    bootstrapAuth();
    const onExpired = () => {
      // Only redirect if the user is currently on an authed page; otherwise
      // they're already at /signin.
      const path = window.location.pathname;
      const isAuthed = path !== "/signin" && path !== "/signup" && path !== "/verify";
      if (isAuthed) window.location.href = "/signin?expired=1";
    };
    window.addEventListener("auth-expired", onExpired);
    return () => window.removeEventListener("auth-expired", onExpired);
  }, []);
  // suppress unused-var warning for useLocation
  void loc;
  return <>{children}</>;
}

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
      <AuthBridge>
        <Routes>
          {/* Public */}
          <Route path="/signin" element={<SignIn />} />
          <Route path="/signup" element={<SignUp />} />
          <Route path="/verify" element={<Verify />} />
          <Route path="/forgot" element={<ForgotPassword />} />
          <Route path="/reset" element={<ResetPassword />} />
          <Route path="/device/approve" element={<DeviceApprove />} />

          {/* Protected (wrapped in AppShell) */}
          <Route
            path="/"
            element={
              <RequireAuth>
                <AppShell><Dashboard /></AppShell>
              </RequireAuth>
            }
          />
          <Route
            path="/account"
            element={
              <RequireAuth>
                <AppShell><Account /></AppShell>
              </RequireAuth>
            }
          />
          <Route
            path="/devices"
            element={
              <RequireAuth>
                <AppShell><Devices /></AppShell>
              </RequireAuth>
            }
          />
          <Route
            path="/mcp"
            element={
              <RequireAuth>
                <AppShell><MCP /></AppShell>
              </RequireAuth>
            }
          />
          <Route
            path="/llm-keys"
            element={
              <RequireAuth>
                <AppShell><LLMKeys /></AppShell>
              </RequireAuth>
            }
          />
          <Route
            path="/audit"
            element={
              <RequireAuth>
                <AppShell><Audit /></AppShell>
              </RequireAuth>
            }
          />
          <Route
            path="/agent"
            element={
              <RequireAuth>
                <AppShell><Agent /></AppShell>
              </RequireAuth>
            }
          />
          <Route
            path="/settings"
            element={
              <RequireAuth>
                <AppShell><Settings /></AppShell>
              </RequireAuth>
            }
          />
          <Route
            path="/ssh-terminal"
            element={
              <RequireAuth>
                <AppShell><SSHTerminal /></AppShell>
              </RequireAuth>
            }
          />
          <Route
            path="/ssh-connections"
            element={
              <RequireAuth>
                <AppShell><SSHConnections /></AppShell>
              </RequireAuth>
            }
          />

          <Route path="/404" element={<NotFound />} />
          <Route path="*" element={<Navigate to="/404" replace />} />
        </Routes>
      </AuthBridge>
    </BrowserRouter>
  );
}