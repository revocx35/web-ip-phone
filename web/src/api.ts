// REST client. Cookies carry the session; X-Requested-With is the server's CSRF check.

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message);
  }
}

type Listener = (e: ApiError) => void;
const authListeners = new Set<Listener>();

/** Called when the session is gone (401) or restricted (password/2FA required). */
export function onAuthProblem(l: Listener): () => void {
  authListeners.add(l);
  return () => authListeners.delete(l);
}

export async function api<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { 'X-Requested-With': 'webphone' };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  let res: Response;
  try {
    res = await fetch('/api/v1' + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: 'same-origin',
      cache: 'no-store',
    });
  } catch {
    throw new ApiError(0, 'network', 'Cannot reach the server');
  }
  const text = await res.text();
  let data: any = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  if (!res.ok) {
    const err = new ApiError(res.status, data?.code ?? 'error', data?.error ?? `Request failed (${res.status})`);
    if (res.status === 401 || err.code === 'password_change_required' || err.code === '2fa_required') {
      if (!path.startsWith('/auth/')) authListeners.forEach((l) => l(err));
    }
    throw err;
  }
  return data as T;
}

export const get = <T>(p: string) => api<T>('GET', p);
export const post = <T>(p: string, b?: unknown) => api<T>('POST', p, b ?? {});
export const put = <T>(p: string, b: unknown) => api<T>('PUT', p, b);
export const patch = <T>(p: string, b: unknown) => api<T>('PATCH', p, b);
export const del = <T>(p: string) => api<T>('DELETE', p);

// ---- types shared with the server ---------------------------------------------------

export interface Me {
  id: number;
  username: string;
  displayName: string;
  isAdmin: boolean;
  totpEnabled: boolean;
  mustChangePassword: boolean;
  mustEnroll2fa: boolean;
  twoFaRequired: boolean;
  recoveryCodesLeft: number;
  passwordMinLength: number;
  sessionId: number;
}

export interface RegState {
  status: 'off' | 'registering' | 'registered' | 'failed';
  error?: string;
  expires?: string;
}

export interface PhoneView {
  id: number;
  label: string;
  sipUser: string;
  displayName: string;
  pbxId: number;
  pbxName: string;
  owned: boolean;
  register: boolean;
  reg: RegState;
  online: boolean;
}

export type CallState = 'calling' | 'ringing' | 'early' | 'incoming' | 'active' | 'ended';

export interface CallView {
  id: string;
  phoneId: number;
  direction: 'in' | 'out';
  remote: string;
  remoteName: string;
  state: CallState;
  codec: string;
  hold: boolean;
  remoteHold: boolean;
  startedAt: string;
  answeredAt?: string;
  endReason?: string;
  endStatus?: string;
  attached: boolean;
  mine: boolean;
}

export interface CallRecord {
  id: number;
  userId: number;
  phoneId: number;
  username: string;
  phoneLabel: string;
  pbxName: string;
  direction: 'in' | 'out';
  remote: string;
  remoteName: string;
  startedAt: string;
  answeredAt: string | null;
  endedAt: string | null;
  status: string;
  reason: string;
  sipCode: number;
}

export interface Phone {
  id: number;
  pbxId: number;
  ownerId: number;
  label: string;
  sipUser: string;
  authUser: string;
  displayName: string;
  register: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface PBX {
  id: number;
  name: string;
  host: string;
  port: number;
  transport: 'udp' | 'tcp' | 'tls';
  domain: string;
  tlsVerify: boolean;
  codecs: string[];
  dtmfMode: 'rfc4733' | 'info';
  registerExpiry: number;
  enabled: boolean;
}

export interface User {
  id: number;
  username: string;
  displayName: string;
  isAdmin: boolean;
  disabled: boolean;
  totpEnabled: boolean;
  mustChangePassword: boolean;
  createdAt: string;
  connections?: number;
}

export interface Access {
  pbxId: number;
  mode: 'any' | 'selected';
  dialRules: string;
  phoneIds: number[];
}

export interface Settings {
  require2fa: 'off' | 'admins' | 'all';
  passwordMinLength: number;
  webSessionIdleMinutes: number;
  webSessionMaxHours: number;
  appSessionIdleDays: number;
  appSessionMaxDays: number;
  maxCallsPerUser: number;
  callHistoryDays: number;
  auditLogDays: number;
}

export interface Session {
  id: number;
  kind: 'web' | 'app';
  name: string;
  createdAt: string;
  lastSeenAt: string;
  expiresAt: string;
  createdIp: string;
  lastIp: string;
  current: boolean;
}

export interface AuditEntry {
  id: number;
  time: string;
  userId: number;
  username: string;
  ip: string;
  action: string;
  target: string;
  details: string;
  success: boolean;
}
