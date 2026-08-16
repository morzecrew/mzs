const fs = require('fs');
const spec = fs.readFileSync(require('path').join(__dirname, '..', '..', '..', '..', 'SPEC.md'), 'utf8').split('\n');
const reg = JSON.parse(fs.readFileSync(require('path').join(__dirname, 'registry.json'), 'utf8'));

const start = spec.findIndex(l => /^## 12\. Standard library/.test(l));
const end = spec.findIndex((l, i) => i > start && /^## 13\./.test(l));
const body = spec.slice(start, end);

// Split on unescaped pipes only: cells legitimately contain `\|` (e.g. the bool row).
const cells = l => l.split(/(?<!\\)\|/).slice(1, -1).map(s => s.trim().replace(/\\\|/g, '|'));
const names = c => (c.match(/`([^`]+)`/g) || []).map(s => s.slice(1, -1));
const strip = s => (s || '').replace(/\*\*A\*\*/g, '').trim();

const docs = {};       // "receiver|name" -> {sig, sem, ex}
const modDocs = {};    // "module|member" -> {sig, sem, ex}
// Which receivers a subsection's rows describe when its table has no Receiver column.
// Keyed on the §12.N number, because the headings are prose and have been renamed before
// — that is exactly how this generator silently lost its coverage once.
const SECTION_RECV = {
  '1':  ['builtin', 'any'],
  '2':  ['string'],
  '3':  ['array'],
  '4':  ['dict'],
  '5':  ['int', 'float'],
  '6':  ['regex'],
  '7':  ['builtin', 'any'],
  '9':  ['nil', 'bool', 'fn'],
  '10': ['range'],
  '12': ['task'],
  '14': ['seq'],
};

let hdr = null, prev = null, defRecv = ['builtin'], sectionModule = null;

for (const line of body) {
  const s = line.match(/^### 12\.(\d+) (.+)$/);
  if (s) {
    hdr = null; prev = null;
    defRecv = SECTION_RECV[s[1]] || [];
    // "The `http` module" documents one module in a table of bare member names.
    const m = s[2].match(/`(\w+)`\s+module/);
    sectionModule = m ? m[1] : null;
    continue;
  }
  if (!line.startsWith('|')) { hdr = null; continue; }
  if (/^\|[\s:-]+\|/.test(line) && /-/.test(line)) continue;      // separator
  const c = cells(line);
  if (!c.length) continue;

  // A header row defines the column layout for the table that follows.
  if (/^(Name|Receiver|Method|Module|Member)$/i.test(c[0])) {
    hdr = c.map(h => h.toLowerCase());
    prev = null;
    continue;
  }
  if (!hdr) continue;

  const col = n => { const i = hdr.indexOf(n); return i < 0 ? '' : (c[i] || ''); };

  // §12.8's table is `| Module | Member | …` and §12.11's is `| Member | …` under a
  // heading that names the module. Both describe module members, not methods.
  if (hdr.includes('member')) {
    const mod = (names(col('module'))[0] || sectionModule || '').trim();
    const sig = strip(col('signature')), sem = strip(col('semantics')), ex = strip(col('example'));
    // One cell may hold a whole family: `sqrt cbrt sin cos …` is thirteen members with
    // one signature between them.
    const members = names(col('member')).flatMap(x => x.split(/\s+/)).filter(Boolean);
    for (const n of members) {
      if (mod) modDocs[`${mod}|${n}`] = { sig, sem, ex };
    }
    continue;
  }

  const recvCell = col('receiver');
  const nameCell = col('method') || col('name') || col('methods');
  const ns = names(nameCell);
  if (!ns.length) continue;

  // A prose row (§12.9) lists several methods with inline descriptions in one cell.
  if (hdr.includes('methods') && ns.length > 1) {
    const recvP = (recvCell ? recvCell.replace(/`/g, '').trim() : (defRecv[0] || 'any'));
    for (const m of nameCell.matchAll(/`([^`]+)`([^`]*)/g)) {
      const nm = m[1].split(/[ (]/)[0];
      const desc = m[2].replace(/^[\s,]+|[\s,]+$/g, '').replace(/^→\s*/, 'returns ');
      docs[`${recvP}|${nm}`] = { sig: m[1] === nm ? '' : m[1], sem: desc, ex: '' };
    }
    continue;
  }

  const sig = strip(col('signature')), sem = strip(col('semantics')), ex = strip(col('example'));
  const isAlias = !sig && !sem && prev;
  const doc = isAlias ? prev : { sig, sem, ex };
  if (!isAlias) prev = doc;

  const recvs = recvCell ? [recvCell.replace(/`/g, '').trim()] : defRecv;
  for (const n of ns) for (const r of recvs) docs[`${r}|${n}`] = doc;
}

// §12.11: `lower`->`downcase` etc. Aliases inherit the target's row verbatim.
for (const line of body) {
  for (const m of line.matchAll(/`([^`]+)`\s*(?:→|->)\s*`([^`]+)`/g)) {
    for (const key of Object.keys(docs)) {
      const [r, n] = key.split('|');
      if (n === m[2] && !docs[`${r}|${m[1]}`]) docs[`${r}|${m[1]}`] = docs[key];
    }
  }
}
// A trailing `!` is an alias (immutable receivers) or the in-place form (Array/Map).
for (const key of Object.keys(docs)) {
  const [r, n] = key.split('|');
  if (!docs[`${r}|${n}!`]) docs[`${r}|${n}!`] = docs[key];
}

const alias = { string:['string'], array:['array'], dict:['dict','map'], map:['map'], int:['int','number','number (int)'],
  float:['float','number','number (float)'], regex:['regex'], time:['time','Time'],
  // §12.10: a Range answers the array rows, so it inherits their prose.
  range:['range','array'],
  // §12.14: a seq's rows have their own table, and where a name is only in §12.3 — the
  // conversions of §12.1 above all — the array prose describes the same operation.
  seq:['seq','array'],
  nil:['nil'], bool:['bool'], fn:['fn','function'], any:['any'] };

const out = { methods:{}, builtins:{}, modules:{}, gates: reg.gates || {}, keywords: reg.keywords || [],
  note:'generated from SPEC.md §12 + the live registry' };
let withDoc = 0; const without = [];

for (const [kind, list] of Object.entries(reg.methods)) {
  out.methods[kind] = {};
  for (const n of list) {
    let d = null;
    for (const r of (alias[kind]||[kind])) if (docs[`${r}|${n}`]) { d = docs[`${r}|${n}`]; break; }
    if (!d) d = docs[`any|${n}`];
    out.methods[kind][n] = d || {};
    d ? withDoc++ : without.push(`${kind}.${n}`);
  }
}
const anyReceiver = n => {
  for (const key of Object.keys(docs)) if (key.endsWith(`|${n}`)) return docs[key];
  return null;
};
for (const n of reg.builtins) {
  const d = docs[`builtin|${n}`] || docs[`any|${n}`] || anyReceiver(n);
  out.builtins[n] = d || {};
  d ? withDoc++ : without.push(`builtin ${n}`);
}
for (const [mod, members] of Object.entries(reg.modules || {})) {
  out.modules[mod] = {};
  for (const n of members) {
    const d = modDocs[`${mod}|${n}`];
    out.modules[mod][n] = d || {};
    d ? withDoc++ : without.push(`${mod}.${n}`);
  }
}

fs.writeFileSync(require('path').join(__dirname, '..', 'api.json'), JSON.stringify(out, null, 1));
const total = Object.values(reg.methods).reduce((a,b)=>a+b.length,0) + reg.builtins.length +
  Object.values(reg.modules || {}).reduce((a,b)=>a+b.length,0);
console.log(`documented ${withDoc}/${total} (${Math.round(withDoc/total*100)}%)`);
console.log(`undocumented (${without.length}): ${without.slice(0,30).join(' ')}`);
