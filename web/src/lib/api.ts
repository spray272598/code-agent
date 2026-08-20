// API client + auth store (localStorage-backed). All HTTP requests go through
// `api<T>()` which:
//   1. attaches Authorization: Bearer <accessToken> if present
//   2. on 401, tries a single refresh (POST /api/v1/auth/refresh) and retries
//   3. on second 401, clears tokens and emits a "auth-expired" event so the
//      router can redirect to /signin
//
// The shape mirrors the Go backend's envelope: { code: "0000", message, data }.

const ACCESS_KEY = "codeagent.access";
const REFRESH_KEY = "codeagent.refresh";
const USER_KEY = "codeagent.user";

export interface AuthUser {
  userId: string;
  orgId: string;
  role: string;
  email?: string;
}

export interface TokenPair {
  accessToken: string;
  refreshToken: string;
  tokenType: "Bearer";
  expiresIn: number;
}

export interface ApiEnvelope<T = unknown> {
  code: string;
  message?: string;
  data?: T;
}

let accessToken: string | null = null;
let refreshToken: string | null = null;
let user: AuthUser | null = null;
let refreshInFlight: Promise<TokenPair | null> | null = null;

export function bootstrapAuth() {
  accessToken = localStorage.getItem(ACCESS_KEY);
  refreshToken = localStorage.getItem(REFRESH_KEY);
  const u = localStorage.getItem(USER_KEY);
  if (u) user = JSON.parse(u);
}

export function getAccessToken(): string | null {
  return accessToken;
}

export function getUser(): AuthUser | null {
  return user;
}

export function setTokens(pair: TokenPair, u?: AuthUser) {
  accessToken = pair.accessToken;
  refreshToken = pair.refreshToken;
  localStorage.setItem(ACCESS_KEY, pair.accessToken);
  localStorage.setItem(REFRESH_KEY, pair.refreshToken);
  if (u) {
    user = u;
    localStorage.setItem(USER_KEY, JSON.stringify(u));
  }
}

export function clearTokens() {
  accessToken = null;
  refreshToken = null;
  user = null;
  localStorage.removeItem(ACCESS_KEY);
  localStorage.removeItem(REFRESH_KEY);
  localStorage.removeItem(USER_KEY);
}

async function tryRefresh(): Promise<TokenPair | null> {
  if (!refreshToken) return null;
  if (refreshInFlight) return refreshInFlight;
  refreshInFlight = (async () => {
    try {
      const res = await fetch("/api/v1/auth/refresh", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: refreshToken }),
      });
      if (!res.ok) return null;
      const env = (await res.json()) as ApiEnvelope<TokenPair>;
      if (env.code !== "0000" || !env.data) return null;
      setTokens(env.data, user ?? undefined);
      return env.data;
    } catch {
      return null;
    } finally {
      refreshInFlight = null;
    }
  })();
  return refreshInFlight;
}

export async function api<T = unknown>(
  path: string,
  init: RequestInit = {},
  opts: { auth?: boolean; raw?: boolean } = {},
): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  if (opts.auth !== false && accessToken) {
    headers.set("Authorization", `Bearer ${accessToken}`);
  }

  const doFetch = async () =>
    fetch(path, { ...init, headers, credentials: "omit" });

  let res = await doFetch();
  if (res.status === 401 && refreshToken) {
    const pair = await tryRefresh();
    if (pair) {
      headers.set("Authorization", `Bearer ${pair.accessToken}`);
      res = await doFetch();
    }
  }
  if (res.status === 401) {
    clearTokens();
    window.dispatchEvent(new CustomEvent("auth-expired"));
    throw new ApiError(401, "unauthorized");
  }
  if (!res.ok) {
    const text = await res.text();
    throw new ApiError(res.status, text || res.statusText);
  }
  if (opts.raw) return (await res.text()) as unknown as T;
  const env = (await res.json()) as ApiEnvelope<T>;
  if (env.code && env.code !== "0000") {
    throw new ApiError(Number(env.code) || 0, env.message || `code=${env.code}`);
  }
  return (env.data ?? (env as unknown as T)) as T;
}

export class ApiError extends Error {
  constructor(public status: number, msg: string) {
    super(msg);
    this.name = "ApiError";
  }
}