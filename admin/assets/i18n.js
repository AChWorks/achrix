// SPDX-License-Identifier: MPL-2.0
"use strict";
(() => {
  // Catalog data is autoescaped text, never inline executable code or HTML.
  const source = document.getElementById("admin-messages");
  if (!source || source.textContent.length > 64 * 1024) throw new Error("Missing or oversized administration messages");
  const data = JSON.parse(source.textContent);
  if (!data || typeof data.domains !== "object" || !data.domains) throw new Error("Invalid administration messages");
  const own = (object, key) => Object.prototype.hasOwnProperty.call(object, key);
  const encoder = new TextEncoder();
  const rules = new Map();
  const name = /^[A-Za-z][A-Za-z0-9_]{0,63}$/;
  function message(domain, key, count) {
    if (!own(data.domains, domain) || !own(data.domains[domain], key)) throw new Error("Unknown message " + domain + "/" + key);
    const value = data.domains[domain][key];
    let form = "other";
    if (count !== null) {
      if (!Number.isSafeInteger(count)) throw new Error("Plural count must be a safe integer");
      if (!rules.has(value.locale)) rules.set(value.locale, new Intl.PluralRules(value.locale));
      form = rules.get(value.locale).select(count);
    }
    return value.forms[form] || value.forms.other;
  }
  function format(domain, key, params, count, nodes) {
    if (!params || typeof params !== "object" || Array.isArray(params)) throw new Error("Invalid message parameters");
    const keys = Object.keys(params);
    if (keys.length > 32 || keys.some(key => !name.test(key))) throw new Error("Invalid message parameters");
    const values = Object.assign(Object.create(null), params);
    function parameter(value) {
      if (value instanceof Node && !nodes) throw new Error("DOM parameters require parts");
      const string = value instanceof Node ? value.textContent : value;
      if (typeof string !== "string") throw new Error("Message parameters must be strings");
      for (let i = 0; i < string.length; i++) {
        const code = string.charCodeAt(i);
        if (code >= 0xd800 && code <= 0xdbff) {
          const next = string.charCodeAt(++i);
          if (!(next >= 0xdc00 && next <= 0xdfff)) throw new Error("Invalid Unicode parameter");
        } else if (code >= 0xdc00 && code <= 0xdfff) throw new Error("Invalid Unicode parameter");
      }
      const size = encoder.encode(string).length;
      if (size > 8 * 1024) throw new Error("Message parameter is too large");
      return size;
    }
    for (const key of keys) parameter(values[key]);
    if (count !== null) {
      if (own(values, "Count")) throw new Error("Count is reserved for plural messages");
      values.Count = String(count);
    }
    let outputBytes = 0;
    const result = [];
    function add(value) {
      outputBytes += parameter(value);
      if (outputBytes > 32 * 1024) throw new Error("Formatted message is too large");
      result.push(value);
    }
    const pattern = message(domain, key, count);
    let literal = "";
    function flush() {
      if (!literal) return;
      // Catalog forms have their own 8 KiB bound. A literal plus escaped braces
      // may exceed a parameter bound only across several chunks, so account
      // for it directly instead of treating it as a caller parameter.
      outputBytes += encoder.encode(literal).length;
      if (outputBytes > 32 * 1024) throw new Error("Formatted message is too large");
      result.push(literal); literal = "";
    }
    for (let i = 0; i < pattern.length;) {
      const char = pattern[i];
      if ((char === "{" || char === "}") && pattern[i + 1] === char) {
        literal += char; i += 2; continue;
      }
      if (char === "{") {
        flush();
        const end = pattern.indexOf("}", i + 1);
        if (end < 0) throw new Error("Invalid message placeholder");
        const key = pattern.slice(i + 1, end);
        if (!name.test(key) || !own(values, key)) throw new Error("Missing message parameter " + key);
        const value = values[key];
        if (value instanceof Node && !nodes) throw new Error("DOM parameters require parts");
        add(value); i = end + 1; continue;
      }
      if (char === "}") throw new Error("Invalid message placeholder");
      literal += char; i++;
    }
    flush();
    if (!nodes) return result.join("");
    const fragment = document.createDocumentFragment();
    for (const part of result) fragment.append(part instanceof Node ? part.cloneNode(true) : document.createTextNode(part));
    return fragment;
  }
  // Browser callers get one formatter for every Module/product domain. DOM
  // nodes are supplied only by trusted compiled callers; catalogs cannot create
  // markup, handlers, scripts, links, or template expressions.
  window.AChrixI18n = Object.freeze({
    text: (domain, key, params = {}, count = null) => format(domain, key, params, count, false),
    parts: (domain, key, params = {}, count = null) => format(domain, key, params, count, true)
  });
})();
