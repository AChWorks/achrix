// SPDX-License-Identifier: MPL-2.0
"use strict";
(() => {
  const authPath = document.body.dataset.authPath;
  const status = document.getElementById("status");
  let busy = false;
  const text = key => window.AChrixI18n.text("admin", key);
  function announce(message, failure = false) {
    status.textContent = message;
    status.setAttribute("role", failure ? "alert" : "status");
    status.focus();
  }
  const messages = {
    400: text("input_invalid"),
    401: text("session_expired"),
    404: text("record_missing"),
    403: text("permission_denied"),
    409: text("record_conflict"),
    413: text("input_large"),
    429: text("service_busy")
  };
  const unknown = text("outcome_unknown");
  function lock(on) {
    busy = on;
    document.querySelectorAll("form[data-admin-form],form[data-login]").forEach(form => {
      form.setAttribute("aria-busy", String(on));
      form.querySelectorAll("button[type=submit]").forEach(button => { button.disabled = on || button.dataset.denied === "true"; });
    });
    const logout = document.getElementById("logout"); if (logout) logout.disabled = on;
  }
  async function csrf(signal) {
    // Identity requires origin evidence on this GET; the document otherwise sends
    // no referrer. Supply only our origin root, never the current page or query.
    const response = await fetch(authPath + "/csrf", {mode:"same-origin",redirect:"error",referrer:location.origin+"/",referrerPolicy:"same-origin",credentials:"same-origin",headers:{"X-Identity-Request":"1"},cache:"no-store",signal});
    if (!response.ok) throw {status:response.status};
    const value = await response.json();
    if (typeof value.csrf !== "string") throw new Error("unavailable");
    return value.csrf; // Request-local only; never a URL, DOM attribute or Web Storage.
  }
  document.addEventListener("input", event => {
    if (event.target.matches("input[type=password]")) event.target.setCustomValidity("");
  });
  // Delegation also covers bounded Module-owned rows added after a list response.
  document.addEventListener("submit", async event => {
      const form = event.target;
      if (!form.matches("form[data-admin-form],form[data-login]")) return;
      event.preventDefault(); if (busy) return;
      for (const input of form.querySelectorAll("input[type=password]")) {
        const minimum = form.hasAttribute("data-login") ? 1 : 15;
        const count = Array.from(input.value).length;
        if (count < minimum || count > 256 || new TextEncoder().encode(input.value).length > 1024) {
          input.setCustomValidity(text("password_length"));
          input.reportValidity(); return;
        }
      }
      const confirmation = form.querySelector("[data-confirm]");
      if (confirmation && !confirmation.checked) { announce(text("confirm_change"),true); confirmation.focus(); return; }
      lock(true); announce(text("working"));
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), 15000);
      let sent = false;
      try {
        const login = form.hasAttribute("data-login");
        const headers = {"X-Identity-Request":"1"};
        if (!login) headers["X-CSRF-Token"] = await csrf(controller.signal);
        let body;
        if (form.hasAttribute("data-multipart")) {
          const file = form.querySelector("input[type=file]");
          if (!file || file.files.length !== 1 || file.files[0].size === 0) throw {status:400};
          if (file.files[0].size > 10 * 1024 * 1024) throw {status:413};
          body = new FormData(form); // The browser supplies the multipart boundary.
        } else {
          headers["Content-Type"] = "application/json";
          const values = {};
          for (const control of form.elements) {
            if (!control.name || control.disabled || control.hasAttribute("data-confirm")) continue;
            values[control.name] = control.type === "checkbox" ? control.checked : control.value;
          }
          body = JSON.stringify(values); // int64 revisions remain exact decimal strings.
        }
        sent = true;
        const response = await fetch(form.action, {method:"POST",credentials:"same-origin",headers,body,cache:"no-store",signal:controller.signal});
        // Clear credentials even when the response is lost; never render them back.
        form.querySelectorAll("input[type=password]").forEach(input => { input.value = ""; });
        if (!response.ok) {
          let detail;
          try { detail = await response.json(); } catch (_) { /* Safe status remains useful. */ }
          throw {status:response.status, detail};
        }
        const result = response.status === 204 ? {} : await response.json();
        if (login) { location.assign("/admin/"); return; }
        announce(text("completed"));
        form.dispatchEvent(new CustomEvent("admin:success",{detail:result,bubbles:true}));
        if (confirmation) confirmation.checked = false;
      } catch (error) {
        form.querySelectorAll("input[type=password]").forEach(input => { input.value = ""; });
        const uncertain = sent && !form.hasAttribute("data-read-only") && !messages[error.status];
        const knownMessage = error.status === 404 && form.dataset.operation === "lookup-login" ? text("login_missing") : messages[error.status];
        announce(knownMessage || (uncertain ? unknown : text("service_unavailable")),true);
        form.dispatchEvent(new CustomEvent("admin:failure",{detail:{status:error.status||0,unknown:uncertain,server:error.detail},bubbles:true}));
      } finally { clearTimeout(timer); lock(false); }
  });
  const logout = document.getElementById("logout");
  if (logout) logout.addEventListener("click", async () => {
    if (busy) return; lock(true);
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 15000);
    try { const token = await csrf(controller.signal); const response = await fetch(authPath+"/logout",{method:"POST",credentials:"same-origin",headers:{"X-Identity-Request":"1","X-CSRF-Token":token},cache:"no-store",signal:controller.signal}); if (!response.ok) throw {status:response.status}; location.assign("/admin/login"); }
    catch(error) { announce(messages[error.status]||unknown,true); }
    finally { clearTimeout(timer); lock(false); }
  });
  lock(false);
})();
