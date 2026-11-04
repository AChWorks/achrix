// SPDX-License-Identifier: MPL-2.0
"use strict";
(() => {
  if (customElements.get("achrix-media-picker")) return;
  const root = "/admin/media/picker";
  const idPattern = /^[A-Z2-7]{26}$/;
  const cursorPattern = /^[A-Za-z0-9_-]{36}$/;
  const mimePattern = /^[a-z0-9.+-]+\/[a-z0-9.+-]+$/;
  const revisionPattern = /^[1-9][0-9]{0,18}$/;
  const text = (key, params) => window.AChrixI18n.text("media", "picker." + key, params);
  const make = (tag, content) => {
    const node = document.createElement(tag);
    if (content !== undefined) node.textContent = content;
    return node;
  };
  const valid = (a) => a && typeof a === "object" && idPattern.test(a.id) &&
    typeof a.filename === "string" && a.filename.length <= 256 &&
    typeof a.mime === "string" && mimePattern.test(a.mime) &&
    Number.isSafeInteger(a.size) && a.size >= 0 && a.size <= 10 * 1024 * 1024 &&
    Number.isSafeInteger(a.width) && a.width >= 0 &&
    Number.isSafeInteger(a.height) && a.height >= 0 &&
    typeof a.revision === "string" && revisionPattern.test(a.revision) &&
    a.state === "ready";

  class MediaPicker extends HTMLElement {
    connectedCallback() {
      if (this.initialized) return;
      this.initialized = true;
      const raw = this.getAttribute("accept");
      const types = raw ? raw.split(",").map(value => value.trim()) : [];
      this.accepts = !raw ? null : new Set(types.length <= 32 && types.every(type => mimePattern.test(type)) ? types : []);
      this.details = make("details");
      this.summary = make("summary", text("open"));
      this.details.append(this.summary);
      const controls = make("div");
      this.refresh = make("button", text("refresh"));
      this.refresh.type = "button";
      this.next = make("button", text("next"));
      this.next.type = "button";
      this.next.hidden = true;
      controls.append(this.refresh, this.next);
      this.list = make("ul");
      this.list.setAttribute("aria-label", text("results"));
      this.notice = make("p");
      this.notice.setAttribute("role", "status");
      this.notice.setAttribute("aria-live", "polite");
      this.result = make("p");
      this.result.setAttribute("role", "status");
      this.result.setAttribute("aria-live", "polite");
      this.details.append(controls, this.notice, this.list);
      this.append(this.details, this.result);
      this.refresh.addEventListener("click", () => this.page(""));
      this.next.addEventListener("click", () => this.page(this.nextCursor));
      this.details.addEventListener("toggle", () => {
        if (this.details.open) this.page("");
        else if (this.controller) this.controller.abort();
      });
      this.addEventListener("keydown", event => {
        if (event.key === "Escape" && this.details.open) {
          event.preventDefault();
          this.details.open = false;
          this.summary.focus();
        }
      });
    }
    disconnectedCallback() {
      if (this.controller) this.controller.abort();
    }
    async request(path) {
      if (this.controller) this.controller.abort();
      const controller = new AbortController();
      this.controller = controller;
      const timeout = setTimeout(() => controller.abort(), 9000);
      try {
        const response = await fetch(root + path, {
          method: "GET", credentials: "same-origin", mode: "same-origin", redirect: "error",
          referrerPolicy: "no-referrer", cache: "no-store", headers: {Accept: "application/json"}, signal: controller.signal
        });
        if (!response.ok) {
          let code = "";
          try { code = (await response.json()).code; } catch (_) { /* No trusted error body. */ }
          throw new Error(code === "permission_denied" ? "denied" : code === "not_found" ? "stale" : "unavailable");
        }
        const data = await response.json();
        if (this.controller !== controller || !this.isConnected || !this.details.open) return null;
        return data;
      } finally {
        clearTimeout(timeout);
        if (this.controller === controller) this.controller = null;
      }
    }
    message(key) {
      this.notice.textContent = text(key);
      this.notice.setAttribute("role", key === "unavailable" || key === "denied" || key === "stale" ? "alert" : "status");
    }
    async page(cursor) {
      if (this.busy || !this.details.open) return;
      if (cursor && !cursorPattern.test(cursor)) { this.message("unavailable"); return; }
      this.busy = true;
      this.refresh.disabled = true;
      this.next.disabled = true;
      this.message("loading");
      try {
        const data = await this.request("/page" + (cursor ? "/" + cursor : ""));
        if (!data) return;
        if (!Array.isArray(data.assets) || data.assets.length > 25 ||
            typeof data.next_cursor !== "string" ||
            (data.next_cursor && !cursorPattern.test(data.next_cursor)) ||
            data.assets.some(a => !valid(a))) throw new Error("unavailable");
        this.nextCursor = data.next_cursor;
        this.list.replaceChildren();
        for (const asset of data.assets) {
          if (this.accepts && !this.accepts.has(asset.mime)) continue;
          const item = make("li");
          const select = make("button");
          select.type = "button";
          select.append(document.createTextNode(text("select") + " "));
          const name = make("bdi", asset.filename);
          name.dir = "auto";
          select.append(name);
          const detail = make("span", " · " + asset.mime + " · " + asset.size + " B");
          detail.dir = "ltr";
          item.append(select, detail);
          select.addEventListener("click", () => this.choose(asset.id));
          this.list.append(item);
        }
        this.next.hidden = !this.nextCursor;
        this.message(this.list.childElementCount ? "ready" : "empty");
      } catch (error) {
        if (error.name !== "AbortError") this.message(error.message === "denied" ? "denied" : "unavailable");
        this.list.replaceChildren();
        this.next.hidden = true;
      } finally {
        this.busy = false;
        this.refresh.disabled = false;
        this.next.disabled = false;
      }
    }
    async choose(id) {
      if (this.busy || !this.details.open || !idPattern.test(id)) return;
      this.busy = true;
      this.message("loading");
      try {
        const data = await this.request("/asset/" + id);
        if (!data) return;
        const asset = data.asset;
        if (!valid(asset) || asset.id !== id || this.accepts && !this.accepts.has(asset.mime)) {
          this.message("stale");
          return;
        }
        // Metadata is only a choice, not a capability token or a product write.
        const selection = Object.freeze({id:asset.id, filename:asset.filename, mime:asset.mime,
          size:asset.size, width:asset.width, height:asset.height, revision:asset.revision});
        this.details.open = false;
        this.result.textContent = text("selected", {Filename:asset.filename});
        this.dispatchEvent(new CustomEvent("achrix:media-selected", {detail:selection, bubbles:true}));
        this.summary.focus();
      } catch (error) {
        if (error.name !== "AbortError") this.message(error.message === "denied" ? "denied" : error.message === "stale" ? "stale" : "unavailable");
      } finally {
        this.busy = false;
      }
    }
  }
  customElements.define("achrix-media-picker", MediaPicker);
})();
