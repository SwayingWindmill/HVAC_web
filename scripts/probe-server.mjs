import { readFile } from 'node:fs/promises';

const res = await fetch('http://127.0.0.1:5176/');
console.log('Status:', res.status);
const text = await res.text();
console.log('Body:', text.slice(0, 500));
