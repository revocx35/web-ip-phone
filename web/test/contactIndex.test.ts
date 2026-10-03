// node --test (Node 24 strips the types). Same cases as android/.../ContactIndexTest.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { canonNumber, ContactIndex } from '../src/contactIndex.ts';

const contact = (id: number, name: string, numbers: string[], favorite = false) => ({
  id, name, favorite, shared: false, editable: true, createdAt: '', updatedAt: '',
  numbers: numbers.map((number) => ({ label: '', number })),
});

const book = new ContactIndex([
  contact(1, 'Alice', ['+49 151 1234567', '1005']),
  contact(2, 'Bob', ['030 9876543']),
  contact(3, 'José Núñez', ['john.doe-2']),
  contact(4, 'Reception', ['2001'], true),
  contact(5, 'Reception copy', ['2001']),
]);

test('canonical numbers', () => {
  assert.equal(canonNumber('+49 (151) 123-45.67'), '+491511234567');
  assert.equal(canonNumber('John.Doe-2'), 'john.doe-2');
});

test('lookup: exact, digits, last 7 digits', () => {
  assert.equal(book.lookup('1005')?.contact.name, 'Alice');
  assert.equal(book.lookup('+491511234567')?.contact.name, 'Alice');
  assert.equal(book.lookup('00491511234567')?.contact.name, 'Alice');
  assert.equal(book.lookup('01511234567')?.contact.name, 'Alice');
  assert.equal(book.lookup('+49309876543')?.contact.name, 'Bob');
  assert.equal(book.lookup('JOHN.DOE-2')?.contact.name, 'José Núñez');
  assert.equal(book.lookup('1006'), undefined);
  assert.equal(book.lookup('005'), undefined); // short numbers only match exactly
  assert.equal(book.lookup(''), undefined);
  assert.equal(book.lookup('anonymous'), undefined);
});

test('favorites win when numbers collide', () => {
  assert.equal(book.lookup('2001')?.contact.name, 'Reception');
});

test('search by name (without accents) and number', () => {
  assert.deepEqual(book.search('jose').map((c) => c.id), [3]);
  assert.deepEqual(book.search('98765').map((c) => c.id), [2]);
  assert.deepEqual(book.search('151 123').map((c) => c.id), [1]);
  assert.equal(book.search('').length, 5);
});

test('keypad suggestions', () => {
  assert.equal(book.suggest('1'), undefined); // too short
  assert.equal(book.suggest('100')?.number.number, '1005');
  assert.equal(book.suggest('2001')?.contact.name, 'Reception');
  assert.equal(book.suggest('98')?.contact.name, 'Bob'); // inside the number
  assert.equal(book.suggest('777'), undefined);
});
