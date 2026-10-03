import type { ComponentChildren } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { toastError } from '../store';
import { IconUser, IconX } from './icons';

export function Modal(props: { title: string; onClose: () => void; children: ComponentChildren; wide?: boolean; footer?: ComponentChildren }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && props.onClose();
    window.addEventListener('keydown', onKey);
    ref.current?.querySelector<HTMLElement>('input, select, textarea, button.primary')?.focus();
    return () => window.removeEventListener('keydown', onKey);
  }, []);
  return (
    <div class="backdrop" onMouseDown={(e) => e.target === e.currentTarget && props.onClose()}>
      <div class={'modal' + (props.wide ? ' wide' : '')} role="dialog" aria-modal="true" aria-label={props.title} ref={ref}>
        <div class="modal-head">
          <h2>{props.title}</h2>
          <button class="btn ghost icon-btn" onClick={props.onClose} aria-label="Close">
            <IconX />
          </button>
        </div>
        {props.children}
        {props.footer && <div class="modal-foot">{props.footer}</div>}
      </div>
    </div>
  );
}

/** Initials of a name for avatars (a person icon for numbers). */
export function Initials({ name }: { name: string }) {
  const s = name.trim();
  if (!/\p{L}/u.test(s)) return <IconUser />;
  const parts = s.split(/\s+/).filter((p) => /\p{L}/u.test(p));
  return <>{(parts.length > 1 ? parts[0][0] + parts[1][0] : parts[0][0]).toUpperCase()}</>;
}

export function Field(props: { label: string; hint?: ComponentChildren; children: ComponentChildren }) {
  return (
    <div class="field">
      <label class="field-label">
        <span class="label">{props.label}</span>
        {props.children}
      </label>
      {props.hint && <span class="hint">{props.hint}</span>}
    </div>
  );
}

type InputProps = {
  value: string;
  onValue: (v: string) => void;
  type?: string;
  class?: string;
  placeholder?: string;
  autocomplete?: string;
  required?: boolean;
  minLength?: number;
  maxLength?: number;
  autofocus?: boolean;
  inputMode?: 'text' | 'numeric' | 'tel' | 'email' | 'url' | 'search' | 'decimal' | 'none';
  spellcheck?: boolean;
  readOnly?: boolean;
};

export function Input({ value, onValue, class: cls, ...rest }: InputProps) {
  // Preact's input typings are a union keyed on `type`; the props above are all valid for text-like inputs.
  const attrs = rest as Record<string, unknown>;
  return <input class={cls ?? 'input'} value={value} onInput={(e) => onValue((e.target as HTMLInputElement).value)} {...attrs} />;
}

export function Check(props: { checked: boolean; onChange: (v: boolean) => void; label: ComponentChildren; hint?: string; disabled?: boolean }) {
  return (
    <label class="check">
      <input type="checkbox" checked={props.checked} disabled={props.disabled} onChange={(e) => props.onChange((e.target as HTMLInputElement).checked)} />
      <span>
        {props.label}
        {props.hint && <div class="small muted">{props.hint}</div>}
      </span>
    </label>
  );
}

/** Button that runs an async action, shows progress and reports errors. */
export function AsyncButton(props: {
  class?: string;
  onClick: () => Promise<unknown> | unknown;
  children: ComponentChildren;
  disabled?: boolean;
  type?: 'button' | 'submit';
  title?: string;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <button
      type={props.type ?? 'button'}
      class={'btn ' + (props.class ?? '')}
      disabled={busy || props.disabled}
      title={props.title}
      onClick={async (e) => {
        e.preventDefault();
        setBusy(true);
        try {
          await props.onClick();
        } catch (err) {
          toastError(err);
        } finally {
          setBusy(false);
        }
      }}
    >
      {busy ? '…' : props.children}
    </button>
  );
}

export function RegBadge({ status, error, register = true }: { status: string; error?: string; register?: boolean }) {
  const map: Record<string, [string, string]> = {
    registered: ['ok', 'Registered'],
    registering: ['warn', 'Registering…'],
    failed: ['bad', 'Registration failed'],
    off: ['', 'Not registered (nobody online)'],
  };
  const [cls, text] = register ? (map[status] ?? ['', status]) : ['', 'Outgoing calls only'];
  return (
    <span class={'badge ' + cls} title={error}>
      {text}
    </span>
  );
}

export function fmtTime(iso?: string | null) {
  if (!iso) return '';
  const d = new Date(iso);
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  return sameDay ? d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : d.toLocaleString([], { dateStyle: 'short', timeStyle: 'short' });
}

export function fmtDuration(sec: number) {
  sec = Math.max(0, Math.floor(sec));
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  const mm = String(m).padStart(h ? 2 : 1, '0');
  return (h ? h + ':' : '') + mm + ':' + String(s).padStart(2, '0');
}

export function useInterval(fn: () => void, ms: number) {
  const saved = useRef(fn);
  saved.current = fn;
  useEffect(() => {
    const t = setInterval(() => saved.current(), ms);
    return () => clearInterval(t);
  }, [ms]);
}

/** Confirmation dialog state helper. */
export function useConfirm() {
  const [state, setState] = useState<{ text: string; action: () => Promise<unknown> | void; danger?: boolean } | null>(null);
  const dialog = state && (
    <Modal
      title="Please confirm"
      onClose={() => setState(null)}
      footer={
        <>
          <button class="btn" onClick={() => setState(null)}>
            Cancel
          </button>
          <AsyncButton
            class={state.danger ? 'danger' : 'primary'}
            onClick={async () => {
              await state.action();
              setState(null);
            }}
          >
            Confirm
          </AsyncButton>
        </>
      }
    >
      <p>{state.text}</p>
    </Modal>
  );
  return { confirm: (text: string, action: () => Promise<unknown> | void, danger = true) => setState({ text, action, danger }), dialog };
}
