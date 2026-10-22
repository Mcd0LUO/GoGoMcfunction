// Checks the grammar by TOKENIZING, not by eye.
//
// A TextMate grammar is data. A bad regex does not fail a build -- it silently
// stops colouring, or worse, opens a rule that never closes and turns the rest
// of the file grey. Both of those shipped once: the selector waited for a
// bracket that never came, and a range coloured its second number as a member
// access.
//
// So this runs the real engine (vscode-textmate over oniguruma, the pair the
// editor itself uses) and asserts on the resulting scopes.
const fs = require("fs");
const path = require("path");
const vsctm = require("vscode-textmate");
const oniguruma = require("vscode-oniguruma");

const grammarPath = path.join(__dirname, "syntaxes", "ggm.tmLanguage.json");

async function load() {
  const raw = fs.readFileSync(grammarPath, "utf8");

  // Mechanical checks first, so a broken file reports the actual problem
  // rather than an opaque engine failure.
  const g = JSON.parse(raw);
  const errors = [];
  let expressions = 0;
  (function walk(node, where) {
    if (!node || typeof node !== "object") return;
    for (const key of ["match", "begin", "end"]) {
      const re = node[key];
      if (typeof re !== "string") continue;
      expressions++;
      try { new RegExp(re, "u"); } catch (e) { errors.push(where + " " + key + " -> " + e.message); }
    }
    for (const key of ["patterns", "repository", "captures", "beginCaptures", "endCaptures"]) {
      const v = node[key];
      if (!v) continue;
      if (Array.isArray(v)) v.forEach((c, i) => walk(c, where + "/" + key + "[" + i + "]"));
      else for (const k of Object.keys(v)) walk(v[k], where + "/" + key + "." + k);
    }
  })(g, "root");
  for (const m of raw.matchAll(/"#([A-Za-z0-9_.]+)"/g)) {
    if (!g.repository || !g.repository[m[1]]) errors.push("include #" + m[1] + " has no repository entry");
  }
  if (errors.length) {
    console.error("grammar: " + errors.length + " problem(s)");
    for (const e of errors) console.error("  " + e);
    process.exit(1);
  }

  const wasm = fs.readFileSync(path.join(__dirname, "node_modules", "vscode-oniguruma", "release", "onig.wasm")).buffer;
  const onigLib = oniguruma.loadWASM(wasm).then(() => ({
    createOnigScanner: (s) => new oniguruma.OnigScanner(s),
    createOnigString: (s) => new oniguruma.OnigString(s),
  }));
  const registry = new vsctm.Registry({
    onigLib,
    loadGrammar: async (scope) =>
      scope === "source.ggm" ? vsctm.parseRawGrammar(raw, grammarPath) : null,
  });
  const grammar = await registry.loadGrammar("source.ggm");
  return { grammar, expressions, entries: Object.keys(g.repository).length };
}

// scopesOf tokenizes the lines and returns the scopes of the token at the needle.
function scopesOf(grammar, lines, needle) {
  let state = vsctm.INITIAL;
  for (const line of lines) {
    const r = grammar.tokenizeLine(line, state);
    state = r.ruleStack;
    const at = line.indexOf(needle);
    if (at < 0) continue;
    for (const t of r.tokens) {
      if (t.startIndex <= at && at < t.endIndex) return t.scopes;
    }
  }
  return [];
}

const cases = [
  {
    name: "a range is not a member access",
    lines: ["    X = 1..10"],
    needle: "10",
    want: "constant.numeric.ggm",
  },
  {
    name: "an attribute with an argument is not a call",
    lines: ["[selector(@e[tag=x])]"],
    needle: "selector",
    want: "entity.other.attribute-name.ggm",
  },
  {
    name: "a selector stops at its own bracket",
    lines: ["    decode X from @s.Inventory(0)[flags]"],
    needle: "flags",
    wantNot: "variable.parameter.selector.ggm",
  },
  {
    name: "an unclosed-looking selector does not swallow the next line",
    lines: ['    tellraw(@s, n"{text:hi}")', "    X = 1"],
    needle: "X",
    wantNot: "variable.language.selector.ggm",
  },
  {
    name: "an NBT literal is a string, not a name",
    lines: ['nbt f = p"tag.x" : bits 4'],
    needle: "tag.x",
    want: "string.quoted.nbt.ggm",
  },
  {
    name: "a quoted objective after a selector stays a string",
    lines: ['    let a = @s."arg1"'],
    needle: "arg1",
    want: "string.quoted.double.ggm",
  },
];

(async () => {
  const { grammar, expressions, entries } = await load();
  if (!grammar) {
    console.error("grammar did not load");
    process.exit(1);
  }
  const failures = [];
  for (const c of cases) {
    const scopes = scopesOf(grammar, c.lines, c.needle);
    const deepest = scopes[scopes.length - 1] || "?";
    if (c.want && !scopes.includes(c.want)) {
      failures.push(c.name + ": " + JSON.stringify(c.needle) + " has " + deepest + ", want " + c.want);
    }
    if (c.wantNot && scopes.includes(c.wantNot)) {
      failures.push(c.name + ": " + JSON.stringify(c.needle) + " must not be " + c.wantNot);
    }
  }
  if (failures.length) {
    console.error("grammar: " + failures.length + " case(s) failed");
    for (const f of failures) console.error("  " + f);
    process.exit(1);
  }
  console.log("grammar: " + expressions + " expressions, " + entries + " entries, " + cases.length + " tokenization cases passed");
})();
