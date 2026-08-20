import * as React from "react";
import { Navigate, useLocation } from "react-router-dom";
import { getAccessToken } from "@/lib/api";

// Route guard: any unauthenticated visitor is redirected to /signin, with
// the intended destination captured so we can bounce them back after sign-in.
export function RequireAuth({ children }: { children: React.ReactNode }) {
  const loc = useLocation();
  if (!getAccessToken()) {
    return <Navigate to="/signin" replace state={{ from: loc.pathname }} />;
  }
  return <>{children}</>;
}