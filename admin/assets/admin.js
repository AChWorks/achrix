// SPDX-License-Identifier: MPL-2.0
"use strict";
(() => {
  const fa = document.documentElement.lang === "fa";
  const authPath = document.body.dataset.authPath;
  const status = document.getElementById("status");
  let busy = false;
  const text = (en, faText) => fa ? faText : en;
  function announce(message, failure = false) {
    status.textContent = message;
    status.setAttribute("role", failure ? "alert" : "status");
    status.focus();
  }
  const messages = {
    400: text("Check the input and try again.", "ورودی را بررسی کنید و دوباره تلاش کنید."),
    401: text("Your session expired or could not be verified. Sign in again.", "نشست منقضی شده یا قابل تأیید نیست. دوباره وارد شوید."),
    404: text("No matching record was found. Check the identifier before another change.", "رکوردی پیدا نشد. پیش از تغییر بعدی شناسه را بررسی کنید."),
    403: text("You do not have permission for this operation.", "مجوز این عملیات را ندارید."),
    409: text("The record changed or conflicts with this request. Look it up before another change.", "رکورد تغییر کرده یا درخواست تعارض دارد. پیش از تغییر بعدی دوباره آن را بررسی کنید."),
    413: text("This input is too large.", "اندازهٔ ورودی بیش از حد مجاز است."),
    429: text("The service is busy. Wait before trying again.", "سرویس مشغول است. پیش از تلاش دوباره کمی صبر کنید.")
  };
  const unknown = text("The outcome could not be confirmed. Do not repeat the change automatically. Inspect the account or image library before another change.", "نتیجه قابل تأیید نیست. تغییر را خودکار تکرار نکنید. پیش از تغییر بعدی حساب یا کتابخانهٔ تصاویر را بررسی کنید.");
  function lock(on) {
    busy = on;
    document.querySelectorAll("form[data-admin-form],form[data-login]").forEach(form => {
      form.setAttribute("aria-busy", String(on));
      form.querySelectorAll("button[type=submit]").forEach(button => { button.disabled = on || button.dataset.denied === "true"; });
    });
    const logout = document.getElementById("logout"); if (logout) logout.disabled = on;
  }
  async function csrf(signal) {
    const response = await fetch(authPath + "/csrf", {credentials:"same-origin",headers:{"X-Identity-Request":"1"},cache:"no-store",signal});
    if (!response.ok) throw {status:response.status};
    const value = await response.json();
    if (typeof value.csrf !== "string") throw new Error("unavailable");
    return value.csrf; // Request-local only; never a URL, DOM attribute or Web Storage.
  }
  document.querySelectorAll("form[data-admin-form],form[data-login]").forEach(form => {
    form.querySelectorAll("input[type=password]").forEach(input => input.addEventListener("input", () => input.setCustomValidity("")));
    form.addEventListener("submit", async event => {
      event.preventDefault(); if (busy) return;
      for (const input of form.querySelectorAll("input[type=password]")) {
        const minimum = form.hasAttribute("data-login") ? 1 : 15;
        const count = Array.from(input.value).length;
        if (count < minimum || count > 256 || new TextEncoder().encode(input.value).length > 1024) {
          input.setCustomValidity(text("Check the password length in characters (at most 1024 UTF-8 bytes).", "تعداد کاراکترهای رمز را بررسی کنید (حداکثر ۱۰۲۴ بایت UTF-8)."));
          input.reportValidity(); return;
        }
      }
      const confirmation = form.querySelector("[data-confirm]");
      if (confirmation && !confirmation.checked) { announce(text("Confirm the described change first.", "ابتدا تغییر توضیح‌داده‌شده را تأیید کنید."),true); confirmation.focus(); return; }
      lock(true); announce(text("Working… Please wait.", "در حال انجام… صبر کنید."));
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), 15000);
      let sent = false;
      try {
        const login = form.hasAttribute("data-login");
        const headers = {"Content-Type":"application/json", "X-Identity-Request":"1"};
        if (!login) headers["X-CSRF-Token"] = await csrf(controller.signal);
        const body = {};
        for (const control of form.elements) {
          if (!control.name || control.disabled || control.hasAttribute("data-confirm")) continue;
          body[control.name] = control.type === "checkbox" ? control.checked : control.hasAttribute("data-number") ? Number(control.value) : control.value;
        }
        sent = true;
        const response = await fetch(form.action, {method:"POST",credentials:"same-origin",headers,body:JSON.stringify(body),cache:"no-store",signal:controller.signal});
        // Clear credentials even when the response is lost; never render them back.
        form.querySelectorAll("input[type=password]").forEach(input => { input.value = ""; });
        if (!response.ok) throw {status:response.status};
        const result = response.status === 204 ? {} : await response.json();
        if (login) { location.assign("/admin/"); return; }
        announce(text("Completed. Check the displayed result before another change.", "انجام شد. پیش از تغییر بعدی نتیجهٔ نمایش‌داده‌شده را بررسی کنید."));
        form.dispatchEvent(new CustomEvent("admin:success",{detail:result,bubbles:true}));
        if (confirmation) confirmation.checked = false;
      } catch (error) {
        form.querySelectorAll("input[type=password]").forEach(input => { input.value = ""; });
        const uncertain = sent && !form.hasAttribute("data-read-only") && !messages[error.status];
        const knownMessage = error.status === 404 && form.dataset.operation === "lookup-login" ? text("No account matched the exact login. Check the retained login before another creation attempt.", "حسابی با این شناسهٔ ورود دقیق پیدا نشد. پیش از ایجاد دوباره شناسهٔ نگه‌داشته‌شده را بررسی کنید.") : messages[error.status];
        announce(knownMessage || (uncertain ? unknown : text("The service is unavailable. A read can be attempted again; no change was submitted.", "سرویس در دسترس نیست. می‌توانید بررسی را دوباره انجام دهید؛ تغییری ارسال نشد.")),true);
        form.dispatchEvent(new CustomEvent("admin:failure",{detail:{status:error.status||0,unknown:uncertain},bubbles:true}));
      } finally { clearTimeout(timer); lock(false); }
    });
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
