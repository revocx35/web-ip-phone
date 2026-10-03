// The user's phone book (own + shared contacts), loaded from the server and reloaded when the
// server says it changed. Also resolves caller numbers to contact names.
import { get, type Contact } from './api';
import { Store } from './store';
import { ContactIndex } from './contactIndex';

export { canonNumber, ContactIndex, type ContactHit } from './contactIndex';

export interface ContactsState {
  loaded: boolean;
  error?: string;
  index: ContactIndex;
}

export const contacts = new Store<ContactsState>({ loaded: false, index: new ContactIndex([]) });

let loading: Promise<void> | null = null;
let again = false;
let generation = 0; // bumped on sign-out: answers to older requests are dropped

/** (Re)loads the phone book; calls while a load runs are merged into one more load. */
export function loadContacts(): Promise<void> {
  if (loading) {
    again = true;
    return loading;
  }
  loading = (async () => {
    do {
      again = false;
      const gen = generation;
      try {
        const r = await get<{ contacts: Contact[] }>('/contacts');
        if (gen === generation) contacts.set({ loaded: true, index: new ContactIndex(r.contacts) });
      } catch (e) {
        if (gen === generation) contacts.set((s) => ({ ...s, loaded: true, error: (e as Error).message }));
      }
    } while (again);
  })().finally(() => (loading = null));
  return loading;
}

export function clearContacts() {
  generation++;
  again = false;
  contacts.set({ loaded: false, index: new ContactIndex([]) });
}

/** Name to show for a caller: the contact name, else the name the PBX sent, else the number. */
export function callerName(remote: string, remoteName?: string | null): string {
  return contacts.get().index.lookup(remote)?.contact.name || remoteName || remote;
}
