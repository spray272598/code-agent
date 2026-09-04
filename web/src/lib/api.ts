// API client for the single-operator Agent Harness.
//
// The account/JWT system has been removed; the harness authenticates with a
// single static API key (X-API-Key header). The key is persisted in
// localStorage and defaults to "dev-key" so a fresh local dev instance works
// without configuration.
//
// The shape mirrors the Go backend's envelope: { code: "0000", message, data }.

const API_KEY_STORAGE = "codeagent.apikey";
const DEFAULT_API_KEY = "dev-key";

export interface ApiEnvelope<T = unknown> {
  code: string;
  message?: string;
  data?: T;
}

let apiKey: string | null = null;

// loadApiKey returns the cached key, falling back to localStorage and finally to
// the dev default. The result is always non-empty.
function loadApiKey(): string {
  if (apiKey !== null) return apiKey;
  const stored = localStorage.getItem(API_KEY_STORAGE);
  apiKey = stored && stored.length > 0 ? stored : DEFAULT_API_KEY;
  return apiKey;
}

export function getApiKey(): string {
  return loadApiKey();
}

export function setApiKey(key: string) {
  apiKey = key;
  localStorage.setItem(API_KEY_STORAGE, key);
}

export function clearApiKey() {
  apiKey = null;
  localStorage.removeItem(API_KEY_STORAGE);
}

export async function api<T = unknown>(
  path: string,
  init: RequestInit = {},
  opts: { raw?: boolean } = {},
): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const key = loadApiKey();
  if (key) {
    headers.set("X-API-Key", key);
  }

  let res = await fetch(path, { ...init, headers });
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
