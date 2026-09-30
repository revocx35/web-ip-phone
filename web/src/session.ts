import { get, post, type Me } from './api';
import { navigate, Store } from './store';
import { phone } from './phone/client';

interface SessionState {
  loading: boolean;
  setupRequired: boolean;
  me: Me | null;
}

export const session = new Store<SessionState>({ loading: true, setupRequired: false, me: null });

export async function refreshMe() {
  try {
    const me = await get<Me>('/me');
    session.set((s) => ({ ...s, me, loading: false }));
    return me;
  } catch {
    session.set((s) => ({ ...s, me: null, loading: false }));
    return null;
  }
}

export function signedIn(me: Me) {
  session.set({ loading: false, setupRequired: false, me });
  if (location.pathname === '/login' || location.pathname === '/setup') navigate('/', true);
}

export async function signOut() {
  try {
    await post('/auth/logout');
  } catch {
    /* already gone */
  }
  phone.stop();
  session.set({ loading: false, setupRequired: false, me: null });
  navigate('/', true);
}
