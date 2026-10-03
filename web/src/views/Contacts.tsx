import { useState } from 'preact/hooks';
import { del, post, put, type Contact, type Me } from '../api';
import { toast, toastError, useStore } from '../store';
import { contacts, loadContacts } from '../contacts';
import { AsyncButton, Check, Field, Initials, Input, Modal, useConfirm } from '../components/ui';
import { IconEdit, IconPhone, IconPlus, IconSearch, IconStar, IconX } from '../components/icons';

const LABELS = ['Mobile', 'Work', 'Home', 'Extension', 'Fax'];

/** Contact to create or edit; `prefill` starts a new contact from a call. */
export type ContactEdit = Contact | { prefill: { name: string; number: string } };

export function ContactForm({ me, edit, onClose }: { me: Me; edit: ContactEdit; onClose: () => void }) {
  const existing = 'id' in edit ? edit : null;
  const [name, setName] = useState(existing?.name ?? ('prefill' in edit ? edit.prefill.name : ''));
  const [numbers, setNumbers] = useState(
    existing?.numbers.map((n) => ({ ...n })) ?? [{ label: '', number: 'prefill' in edit ? edit.prefill.number : '' }],
  );
  const [favorite, setFavorite] = useState(existing?.favorite ?? false);
  const [shared, setShared] = useState(existing?.shared ?? false);
  const [error, setError] = useState('');
  const { confirm, dialog } = useConfirm();

  const setNum = (i: number, f: Partial<{ label: string; number: string }>) =>
    setNumbers((list) => list.map((n, j) => (j === i ? { ...n, ...f } : n)));

  const save = async () => {
    setError('');
    const body = {
      name: name.trim(),
      numbers: numbers.filter((n) => n.number.trim()).map((n) => ({ label: n.label.trim(), number: n.number.trim() })),
      favorite,
      shared,
    };
    if (!body.name) return setError('Enter a name');
    if (body.numbers.length === 0) return setError('Enter at least one number');
    try {
      if (existing) await put('/contacts/' + existing.id, body);
      else await post('/contacts', body);
      await loadContacts();
      toast('Contact saved', 'success');
      onClose();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  return (
    <Modal
      title={existing ? 'Edit contact' : 'New contact'}
      onClose={onClose}
      footer={
        <>
          {existing && (
            <button
              class="btn danger"
              style={{ marginRight: 'auto' }}
              onClick={() =>
                confirm(`Delete ${existing.name}${existing.shared ? ' for all users' : ''}?`, async () => {
                  await del('/contacts/' + existing.id);
                  await loadContacts();
                  onClose();
                })
              }
            >
              Delete
            </button>
          )}
          <button class="btn" onClick={onClose}>
            Cancel
          </button>
          <AsyncButton class="primary" onClick={save}>
            Save
          </AsyncButton>
        </>
      }
    >
      <form class="stack" onSubmit={(e) => (e.preventDefault(), save())}>
        <Field label="Name">
          <Input value={name} onValue={setName} maxLength={80} required autocomplete="off" placeholder="Alice Example" />
        </Field>
        <div class="field">
          <span class="field-label">
            <span class="label">Numbers</span>
          </span>
          <datalist id="wip-number-labels">
            {LABELS.map((l) => (
              <option key={l} value={l} />
            ))}
          </datalist>
          {numbers.map((n, i) => (
            <div class="number-row" key={i}>
              <input
                class="input label-in"
                placeholder="Label"
                aria-label="Label"
                list="wip-number-labels"
                maxLength={32}
                value={n.label}
                onInput={(e) => setNum(i, { label: (e.target as HTMLInputElement).value })}
              />
              <input
                class="input grow"
                placeholder="Number or extension"
                aria-label="Number"
                inputMode="tel"
                autocomplete="off"
                maxLength={80}
                value={n.number}
                onInput={(e) => setNum(i, { number: (e.target as HTMLInputElement).value })}
              />
              {numbers.length > 1 && (
                <button type="button" class="btn ghost icon-btn" aria-label="Remove number" onClick={() => setNumbers((l) => l.filter((_, j) => j !== i))}>
                  <IconX />
                </button>
              )}
            </div>
          ))}
          {numbers.length < 10 && (
            <div>
              <button type="button" class="btn ghost small" onClick={() => setNumbers((l) => [...l, { label: '', number: '' }])}>
                <IconPlus /> Add number
              </button>
            </div>
          )}
        </div>
        <Check checked={favorite} onChange={setFavorite} label="Favorite" hint="Favorites are listed first." />
        {me.isAdmin && (
          <Check checked={shared} onChange={setShared} label="Shared with all users" hint="Everyone sees shared contacts; only administrators can change them." />
        )}
        {error && <div class="error-text">{error}</div>}
        <button class="hidden" />
      </form>
      {dialog}
    </Modal>
  );
}

function ContactRow({ c, onCall, onEdit }: { c: Contact; onCall: (n: string) => void; onEdit: () => void }) {
  return (
    <li class="contact">
      <div class="avatar sm">
        <Initials name={c.name} />
      </div>
      <div class="grow">
        <div class="row" style={{ gap: '6px', flexWrap: 'nowrap' }}>
          <span class="ellipsis" style={{ fontWeight: 600 }}>
            {c.name}
          </span>
          {c.shared && <span class="badge">Shared</span>}
        </div>
        <div class="numbers">
          {c.numbers.map((n, i) => (
            <button key={i} class="num-pill" title={'Call ' + n.number} aria-label={`Call ${c.name}${n.label ? ' ' + n.label : ''} ${n.number}`} onClick={() => onCall(n.number)}>
              <IconPhone />
              {n.label && <span class="muted">{n.label}</span>}
              <span>{n.number}</span>
            </button>
          ))}
        </div>
      </div>
      <button
        class={'btn ghost icon-btn star' + (c.favorite ? ' on' : '')}
        title={c.favorite ? 'Remove from favorites' : 'Add to favorites'}
        aria-label={c.favorite ? 'Remove from favorites' : 'Add to favorites'}
        aria-pressed={c.favorite}
        onClick={() =>
          put('/contacts/' + c.id + '/favorite', { favorite: !c.favorite })
            .then(() => loadContacts())
            .catch(toastError)
        }
      >
        <IconStar filled={c.favorite} />
      </button>
      {c.editable && (
        <button class="btn ghost icon-btn" title="Edit" aria-label={'Edit ' + c.name} onClick={onEdit}>
          <IconEdit />
        </button>
      )}
    </li>
  );
}

/** The phone book next to the dialer: search, favorites first, click a number to call it. */
export function ContactsPanel({ onCall, onEdit }: { onCall: (n: string) => void; onEdit: (e: ContactEdit | 'new') => void }) {
  const st = useStore(contacts);
  const [q, setQ] = useState('');
  const found = st.index.search(q);
  const list = q ? found : [...found.filter((c) => c.favorite), ...found.filter((c) => !c.favorite)];
  return (
    <>
      <div class="row" style={{ padding: '0 18px 12px', flexWrap: 'nowrap' }}>
        <div class="search grow">
          <IconSearch />
          <input class="input" type="search" placeholder="Search name or number" aria-label="Search contacts" value={q} onInput={(e) => setQ((e.target as HTMLInputElement).value)} />
        </div>
        <button class="btn primary" aria-label="New contact" title="New contact" onClick={() => onEdit('new')}>
          <IconPlus /> <span class="desktop-only">New</span>
        </button>
      </div>
      {!st.loaded ? (
        <div class="empty">Loading…</div>
      ) : st.error && st.index.list.length === 0 ? (
        <div class="empty error-text">{st.error}</div>
      ) : list.length === 0 ? (
        <div class="empty">{q ? 'No matching contacts' : 'No contacts yet. Add the people you call often.'}</div>
      ) : (
        <ul class="list">
          {list.map((c) => (
            <ContactRow key={c.id} c={c} onCall={onCall} onEdit={() => onEdit(c)} />
          ))}
        </ul>
      )}
    </>
  );
}
