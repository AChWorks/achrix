// SPDX-License-Identifier: MPL-2.0
"use strict";
(() => {
  const authPath = document.body.dataset.authPath;
  const status = document.getElementById("status");
  let busy = false;
  const text = (key, params) => window.AChrixI18n.text("admin", key, params);
  function announce(message, failure = false) {
    status.textContent = message;
    status.setAttribute("role", failure ? "alert" : "status");
    status.focus();
  }
  const messages = {
    input_too_large: "input_large", invalid_input: "input_invalid", invalid_request: "input_invalid",
    authentication_required: "session_expired", authentication_limited: "service_busy",
    permission_denied: "permission_denied", not_found: "record_missing",
    conflict: "record_conflict", authentication_conflict: "record_conflict",
    busy: "service_busy", request_timeout: "request_timeout", body_timeout: "request_timeout",
    request_canceled: "request_canceled", locale_unavailable: "locale_unavailable",
    authorization_unavailable: "authorization_unavailable", authentication_unavailable: "authentication_unavailable"
  };
  const definitive = new Set(["input_too_large","invalid_input","invalid_request","authentication_required","authentication_failed","authentication_limited","permission_denied","not_found","conflict","authentication_conflict","busy"]);
  const fields = {id:"input_id",login:"input_login",password:"input_password",revision:"input_revision",expected_revision:"input_revision",enabled:"input_enabled"};
  const unknown = text("outcome_unknown");
  async function failure(response) {
    let detail;
    try { detail = await response.json(); } catch (_) { /* An unparsed failure cannot establish an outcome. */ }
    return {status:response.status, detail, reference:response.headers.get("X-Request-ID")};
  }
  function failureMessage(error, {sent, readOnly = false, login = false, acknowledged = false, operation = ""}) {
    const detail = error.detail && typeof error.detail === "object" ? error.detail : {};
    const code = typeof detail.code === "string" ? detail.code : "";
    const notExecuted = detail.outcome === "not_executed" || definitive.has(code);
    const uncertain = sent && !readOnly && !acknowledged && !notExecuted;
    let message;
    if (acknowledged) message = text(readOnly ? "read_result_unavailable" : "result_unavailable");
    else if (uncertain) message = unknown;
    else if (code === "authentication_failed") message = text(login ? "credentials_invalid" : "session_expired");
    else if (code === "not_found" && operation === "lookup-login") message = text("login_missing");
    else if (code === "invalid_input" && Object.hasOwn(fields, detail.field)) message = text(fields[detail.field]);
    else if (code === "unavailable" && detail.outcome === "not_executed") message = text("operation_not_started");
    else if (Object.hasOwn(messages, code)) message = text(messages[code]);
    else if (!sent && error.status === 413) message = text("input_large");
    else if (!sent && error.status === 400) message = text("input_invalid");
    else message = text("service_unavailable");
    // A bounded server response identity is display data, not an authorization
    // claim. Never retain credentials, response bodies or arbitrary headers.
    if (typeof error.reference === "string" && /^[A-Za-z0-9_-]{1,128}$/.test(error.reference)) {
      message += " " + text("failure_reference", {Reference:error.reference});
    }
    return {message, uncertain};
  }
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
    if (!response.ok) throw await failure(response);
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
      let sent = false, acknowledged = false;
      const login = form.hasAttribute("data-login");
      try {
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
        if (!response.ok) throw await failure(response);
        acknowledged = true;
        if (login) { location.assign("/admin/"); return; }
        const result = response.status === 204 ? {} : await response.json();
        announce(text(result.warning === "permissions_unavailable" ? "permissions_unavailable" : "completed"), result.warning === "permissions_unavailable");
        form.dispatchEvent(new CustomEvent("admin:success",{detail:result,bubbles:true}));
        if (confirmation) confirmation.checked = false;
      } catch (error) {
        form.querySelectorAll("input[type=password]").forEach(input => { input.value = ""; });
        const state = failureMessage(error, {sent,readOnly:form.hasAttribute("data-read-only"),login,acknowledged,operation:form.dataset.operation});
        announce(state.message,true);
        form.dispatchEvent(new CustomEvent("admin:failure",{detail:{status:error.status||0,unknown:state.uncertain,acknowledged,server:error.detail},bubbles:true}));
      } finally { clearTimeout(timer); lock(false); }
  });
  const logout = document.getElementById("logout");
  if (logout) logout.addEventListener("click", async () => {
    if (busy) return; lock(true);
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 15000);
    let sent = false;
    try { const token = await csrf(controller.signal); sent = true; const response = await fetch(authPath+"/logout",{method:"POST",credentials:"same-origin",headers:{"X-Identity-Request":"1","X-CSRF-Token":token},cache:"no-store",signal:controller.signal}); if (!response.ok) throw await failure(response); location.assign("/admin/login"); }
    catch(error) { announce(failureMessage(error,{sent}).message,true); }
    finally { clearTimeout(timer); lock(false); }
  });
  lock(false);
})();
