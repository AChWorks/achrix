// SPDX-License-Identifier: MPL-2.0
"use strict";
(() => {
  const fa = document.documentElement.lang === "fa";
  const account = document.getElementById("account");
  const empty = document.getElementById("account-empty");
  if (!account || !empty) return;
  function clear() {
    account.hidden = true; empty.hidden = false;
    account.querySelectorAll("form").forEach(form => { form.hidden = true; form.reset(); });
    document.getElementById("account-id").textContent = "";
    document.getElementById("account-login").textContent = "";
    document.getElementById("account-revision").textContent = "";
  }
  document.addEventListener("admin:success", event => {
    const value = event.detail.account;
    if (!value) { clear(); return; }
    document.getElementById("account-id").textContent = value.id;
    document.getElementById("account-login").textContent = value.login;
    document.getElementById("account-revision").textContent = String(value.revision);
    document.getElementById("account-enabled").textContent = value.enabled ? (fa ? "فعال" : "Enabled") : (fa ? "غیرفعال" : "Disabled");
    document.getElementById("lookup-id").value = value.id;
    document.getElementById("lookup-login").value = value.login;
    let any = false;
    account.querySelectorAll("form[data-operation]").forEach(form => {
      const allowed = event.detail.permissions[form.dataset.operation] === true;
      form.hidden = !allowed; any ||= allowed;
      form.elements.id.value = value.id;
      if (form.elements.revision) form.elements.revision.value = String(value.revision);
      if (form.elements.enabled) form.elements.enabled.checked = value.enabled;
    });
    document.getElementById("account-no-actions").hidden = any;
    account.hidden = false; empty.hidden = true;
  });
  document.addEventListener("admin:failure", event => {
    if (event.detail.unknown && event.target.dataset.operation === "create") {
      document.getElementById("lookup-login").value = document.getElementById("new-login").value;
    }
    if (event.target.hasAttribute("data-read-only") || event.detail.unknown || event.detail.status === 401 || event.detail.status === 409 || event.detail.status === 404) clear();
  });
})();
