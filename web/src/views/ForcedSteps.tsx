import type { Me } from '../api';
import { refreshMe, session, signOut } from '../session';
import { ChangePasswordForm, TwoFactorSetup } from './security';

/** Shown after sign-in when an admin requires a new password or 2FA enrolment first. */
export function ForcedSteps({ me }: { me: Me }) {
  return (
    <div class="auth-wrap">
      <div class="card" style={{ width: '100%', maxWidth: '560px' }}>
        <div class="brand" style={{ justifyContent: 'center', marginBottom: '14px' }}>
          <img src="/icon.svg" alt="" />
          Web IP Phone
        </div>
        {me.mustChangePassword ? (
          <>
            <h1>Choose a new password</h1>
            <p class="muted">Your password was set by an administrator. Choose your own before you continue.</p>
            <ChangePasswordForm me={me} onDone={(m) => session.set((s) => ({ ...s, me: m }))} />
          </>
        ) : (
          <>
            <h1>Set up two-factor authentication</h1>
            <p class="muted">Your administrator requires two-factor authentication for your account.</p>
            <TwoFactorSetup onDone={() => refreshMe()} />
          </>
        )}
        <div class="row end" style={{ marginTop: '14px' }}>
          <button class="btn ghost small" onClick={signOut}>
            Sign out
          </button>
        </div>
      </div>
    </div>
  );
}
