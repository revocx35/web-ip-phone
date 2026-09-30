import { render } from 'preact';
import { useEffect } from 'preact/hooks';
import './styles.css';
import { get, onAuthProblem, type Me } from './api';
import { navigate, route, toast, toasts, useStore } from './store';
import { refreshMe, session, signOut } from './session';
import { phone, phoneState } from './phone/client';
import { SetupView } from './views/Setup';
import { LoginView } from './views/Login';
import { ForcedSteps } from './views/ForcedSteps';
import { PhoneView } from './views/Phone';
import { SettingsView } from './views/Settings';
import { AdminView } from './admin/Admin';
import { IncomingCallModal } from './views/Incoming';
import { IconLogout } from './components/icons';

async function boot() {
  try {
    const s = await get<{ required: boolean }>('/setup');
    if (s.required) {
      session.set({ loading: false, setupRequired: true, me: null });
      return;
    }
  } catch {
    /* fall through to login */
  }
  await refreshMe();
}

onAuthProblem((e) => {
  if (e.status === 401) {
    phone.stop();
    session.set((s) => ({ ...s, me: null }));
  } else {
    refreshMe();
  }
});

window.addEventListener('wip-session-ended', (e) => {
  toast('Signed out: ' + ((e as CustomEvent).detail || 'session ended'), 'error', 8000);
  phone.stop();
  session.set((s) => ({ ...s, me: null }));
});

function Toasts() {
  const list = useStore(toasts);
  return (
    <div class="toasts" aria-live="polite">
      {list.map((t) => (
        <div key={t.id} class={'toast ' + t.kind}>
          {t.text}
        </div>
      ))}
    </div>
  );
}

function TopBar({ me }: { me: Me }) {
  const path = useStore(route);
  const ps = useStore(phoneState);
  const link = (href: string, text: string) => (
    <a
      href={href}
      class={(href === '/' ? path === '/' : path.startsWith(href)) ? 'active' : ''}
      onClick={(e) => {
        e.preventDefault();
        navigate(href);
      }}
    >
      {text}
    </a>
  );
  return (
    <header class="topbar">
      <a class="brand" href="/" onClick={(e) => (e.preventDefault(), navigate('/'))}>
        <img src="/icon.svg" alt="" />
        <span>Web IP Phone</span>
      </a>
      <nav class="nav">
        {link('/', 'Phone')}
        {link('/settings', 'Settings')}
        {me.isAdmin && link('/admin', 'Admin')}
      </nav>
      <div class="spacer" />
      {ps.status !== 'online' && ps.status !== 'offline' && (
        <span class="badge warn" title="Connecting to the server">
          {ps.status === 'closed' ? 'Disconnected' : 'Connecting…'}
        </span>
      )}
      <span class="small muted desktop-only" title={me.username}>
        {me.displayName || me.username}
      </span>
      <button class="btn ghost icon-btn" title="Sign out" aria-label="Sign out" onClick={signOut}>
        <IconLogout />
      </button>
    </header>
  );
}

function App() {
  const s = useStore(session);
  const path = useStore(route);

  useEffect(() => {
    boot();
  }, []);

  const ready = s.me && !s.me.mustChangePassword && !s.me.mustEnroll2fa;
  useEffect(() => {
    if (ready && s.me) phone.start(s.me);
    else if (!s.me) phone.stop();
  }, [ready, s.me?.id]);

  if (s.loading) return <div class="auth-wrap muted">Loading…</div>;
  if (s.setupRequired) return <SetupView />;
  if (!s.me) return <LoginView />;
  if (!ready) return <ForcedSteps me={s.me} />;

  let page;
  if (path.startsWith('/settings')) page = <SettingsView me={s.me} />;
  else if (path.startsWith('/admin') && s.me.isAdmin) page = <AdminView me={s.me} />;
  else page = <PhoneView me={s.me} />;
  return (
    <>
      <TopBar me={s.me} />
      <main>{page}</main>
      <IncomingCallModal />
    </>
  );
}

function Root() {
  return (
    <>
      <App />
      <Toasts />
    </>
  );
}

render(<Root />, document.getElementById('app')!);
