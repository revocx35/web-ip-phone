// A tiny observable store and router: enough for this app without extra dependencies.
import { useEffect, useState } from 'preact/hooks';

export class Store<T> {
  private listeners = new Set<() => void>();
  constructor(private value: T) {}
  get(): T {
    return this.value;
  }
  set(v: T | ((old: T) => T)) {
    this.value = typeof v === 'function' ? (v as (o: T) => T)(this.value) : v;
    this.listeners.forEach((l) => l());
  }
  subscribe(l: () => void): () => void {
    this.listeners.add(l);
    return () => this.listeners.delete(l);
  }
}

export function useStore<T>(s: Store<T>): T {
  const [, force] = useState(0);
  useEffect(() => s.subscribe(() => force((n) => n + 1)), [s]);
  return s.get();
}

// ---- router -------------------------------------------------------------------------

export const route = new Store<string>(location.pathname);

export function navigate(path: string, replace = false) {
  if (path === location.pathname) return;
  if (replace) history.replaceState(null, '', path);
  else history.pushState(null, '', path);
  route.set(path);
  window.scrollTo(0, 0);
}

window.addEventListener('popstate', () => route.set(location.pathname));

// ---- toasts -------------------------------------------------------------------------

export interface Toast {
  id: number;
  kind: 'info' | 'error' | 'success';
  text: string;
}

export const toasts = new Store<Toast[]>([]);
let toastSeq = 0;

export function toast(text: string, kind: Toast['kind'] = 'info', ms = 4500) {
  const id = ++toastSeq;
  toasts.set((t) => [...t.slice(-3), { id, kind, text }]);
  setTimeout(() => toasts.set((t) => t.filter((x) => x.id !== id)), ms);
}

export function toastError(e: unknown) {
  toast(e instanceof Error ? e.message : String(e), 'error', 6000);
}
