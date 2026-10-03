// Matching caller numbers to contacts and searching the phone book. Pure logic without imports
// of app state, so it can be tested on its own; android/.../phone/ContactIndex.kt implements the
// same rules.
import type { Contact, ContactNumber } from './api';

export interface ContactHit {
  contact: Contact;
  number: ContactNumber;
}

/** Comparable form of a number: lower case, without the separators people write numbers with.
 *  SIP user names with letters keep their dots and dashes (same rule as the server). */
export function canonNumber(n: string): string {
  const letters = /\p{L}/u.test(n);
  return n.toLowerCase().replace(letters ? /[\s()/]/g : /[\s()/.-]/g, '');
}

const PHONE_LIKE = /^\+?\d+$/;
/** Numbers with at least this many digits also match on their last digits, so +49 30 1234567,
 *  0049301234567 and 0301234567 are the same contact (like Android's caller ID matching). */
const TAIL = 7;

/** Finds contacts by number: exact, then same digits, then same last 7 digits. */
export class ContactIndex {
  private exact = new Map<string, ContactHit>();
  private digits = new Map<string, ContactHit>();
  private tail = new Map<string, ContactHit>();
  readonly list: Contact[];

  constructor(list: Contact[]) {
    this.list = list;
    // Favorites win when two contacts share a number.
    const ordered = [...list.filter((c) => c.favorite), ...list.filter((c) => !c.favorite)];
    for (const contact of ordered) {
      for (const number of contact.numbers) {
        const hit = { contact, number };
        const k = canonNumber(number.number);
        if (!this.exact.has(k)) this.exact.set(k, hit);
        if (!PHONE_LIKE.test(k)) continue;
        const d = k.replace('+', '');
        if (!this.digits.has(d)) this.digits.set(d, hit);
        if (d.length >= TAIL && !this.tail.has(d.slice(-TAIL))) this.tail.set(d.slice(-TAIL), hit);
      }
    }
  }

  lookup(remote: string | undefined | null): ContactHit | undefined {
    if (!remote) return undefined;
    const k = canonNumber(remote);
    const hit = this.exact.get(k);
    if (hit || !PHONE_LIKE.test(k)) return hit;
    const d = k.replace('+', '');
    return this.digits.get(d) ?? (d.length >= TAIL ? this.tail.get(d.slice(-TAIL)) : undefined);
  }

  /** Contacts whose name or numbers contain the query. */
  search(query: string): Contact[] {
    const q = fold(query.trim());
    if (!q) return this.list;
    const qn = canonNumber(query.trim());
    return this.list.filter((c) => fold(c.name).includes(q) || (qn !== '' && c.numbers.some((n) => canonNumber(n.number).includes(qn))));
  }

  /** The contact number that best completes what was typed on the keypad. */
  suggest(typed: string): ContactHit | undefined {
    const t = canonNumber(typed);
    if (t.length < 2) return undefined;
    const exact = this.lookup(typed);
    if (exact) return exact;
    let best: ContactHit | undefined;
    let bestRank = 9;
    for (const contact of this.list) {
      for (const number of contact.numbers) {
        const k = canonNumber(number.number);
        const pos = k.indexOf(t);
        if (pos < 0) continue;
        const rank = (pos === 0 ? 0 : 2) + (contact.favorite ? 0 : 1);
        if (rank < bestRank) [best, bestRank] = [{ contact, number }, rank];
      }
    }
    return best;
  }
}

/** Lower case without diacritics, for name search ("Jose" finds "José"). */
function fold(s: string) {
  return s.normalize('NFD').replace(/\p{M}/gu, '').toLocaleLowerCase();
}
