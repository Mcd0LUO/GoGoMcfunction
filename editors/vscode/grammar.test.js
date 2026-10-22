// Checks the grammar mechanically instead of by eye.
//
// A TextMate grammar is data: a typo in a regex does not fail the build, it
// silently stops colouring, and nobody notices until they open a file. So every
// expression is compiled, every #include is resolved, and every scope name is
// one a theme is likely to have a rule for.
const fs = require("fs");
const path = require("path");

const file = path.join(__dirname, "syntaxes", "ggm.tmLanguage.json");
const g = JSON.parse(fs.readFileSync(file, "utf8"));

let patterns = 0;
const errors = [];

function walk(node, where) {
  if (!node || typeof node !== "object") return;
  for (const key of ["match", "begin", "end"]) {
    const re = node[key];
    if (typeof re !== "string") continue;
    patterns++;
    try {
      new RegExp(re, "u");
    } catch (e) {
      errors.push(where + " " + key + " = " + re + " -> " + e.message);
    }
  }
  for (const key of ["patterns", "repository", "captures", "beginCaptures", "endCaptures"]) {
    const v = node[key];
    if (!v) continue;
    if (Array.isArray(v)) v.forEach((c, i) => walk(c, where + "/" + key + "[" + i + "]"));
    else for (const k of Object.keys(v)) walk(v[k], where + "/" + key + "." + k);
  }
}

walk(g, "root");

// Every include must resolve.
for (const m of JSON.stringify(g).matchAll(/"#([A-Za-z0-9_.]+)"/g)) {
  const name = m[1];
  if (!g.repository || !g.repository[name]) errors.push('include #' + name + ' has no repository entry');
}

// The scope names a theme is likely to know.
const scopes = new Set();
function collect(node) {
  if (!node || typeof node !== "object") return;
  if (typeof node.name === "string") scopes.add(node.name.split(".").slice(0, 2).join("."));
  for (const k of Object.keys(node)) {
    const v = node[k];
    if (Array.isArray(v)) v.forEach(collect);
    else if (v && typeof v === "object") collect(v);
  }
}
collect(g);

if (errors.length) {
  console.error("grammar: " + errors.length + " problem(s)");
  for (const e of errors) console.error("  " + e);
  process.exit(1);
}

if (g.scopeName !== "source.ggm") errors.push("scopeName must be source.ggm");

console.log("grammar: " + patterns + " expressions compiled, " + Object.keys(g.repository).length + " repository entries, " + scopes.size + " scope roots");
console.log("  scopes: " + [...scopes].sort().join(", "));
