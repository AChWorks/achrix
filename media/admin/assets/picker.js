// SPDX-License-Identifier: MPL-2.0
"use strict";
(() => {
  if (customElements.get("achrix-media-picker")) return;
  const defaultRoot = "/admin/media/picker";
  // A literal, same-origin absolute path, never an URL or browser-normalized path.
  // Forbid encoded/ambiguous separators, dot segments, queries and fragments.
  const endpointPattern = /^\/[A-Za-z0-9_-]+(?:\/[A-Za-z0-9_-]+)*$/;
  const endpointRoot = element => {
    const value = element.getAttribute("endpoint");
    return value === null ? defaultRoot :
      value.length <= 256 && endpointPattern.test(value) ? value : null;
  };
  const idPattern = /^[A-Z2-7]{26}$/;
  const cursorPattern = /^[A-Za-z0-9_-]{36}$/;
  const mimePattern = /^[a-z0-9.+-]+\/[a-z0-9.+-]+$/;
  const revisionPattern = /^[1-9][0-9]{0,18}$/;
  const messageKeys = ["open", "results", "refresh", "next", "select", "loading",
    "ready", "empty", "stale", "denied", "unavailable"];
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
    static get observedAttributes() { return ["endpoint"]; }
    attributeChangedCallback(_name, oldValue, newValue) {
      if (oldValue === newValue) return;
      this.reset();
      if (this.isConnected) this.connectedCallback();
    }
    connectedCallback() {
      if (this.initialized || this.initializing) return;
      const root = endpointRoot(this);
      if (!root) {
        this.showError("service_unavailable");
        return;
      }
      // A default (attribute-free) element has no attributeChangedCallback;
      // initialize its generation before accepting the first catalog response.
      this.generation = this.generation || 0;
      const generation = this.generation;
      this.root = root;
      this.initializing = true;
      this.initialize(root, generation).finally(() => {
        if (this.generation === generation) this.initializing = false;
      });
    }
    disconnectedCallback() { this.reset(); }
    reset() {
      this.generation = (this.generation || 0) + 1;
      if (this.controller) this.controller.abort();
      if (this.catalogController) this.catalogController.abort();
      this.controller = null;
      this.catalogController = null;
      this.root = null;
      this.initialized = false;
      this.initializing = false;
      this.busy = false;
      this.details = null;
      this.summary = null;
      this.replaceChildren();
    }
    current(root, generation) {
      return this.isConnected && this.generation === generation && this.root === root;
    }
    showError(key) {
      const notice = make("p", window.AChrixI18n.text("admin", key));
      notice.setAttribute("role", "alert");
      this.replaceChildren(notice);
    }
    async initialize(root, generation) {
      const controller = new AbortController();
      this.catalogController = controller;
      try {
        // The Admin shell only sends the active surface's browser domain.
        // Get Media-owned messages through its authenticated, request-local
        // catalog rather than assuming a product page embeds that domain.
        const response = await fetch(root + "/messages", {
          method: "GET", credentials: "same-origin", mode: "same-origin", redirect: "error",
          referrerPolicy: "no-referrer", cache: "no-store", headers: {Accept: "application/json"},
          signal: controller.signal
        });
        if (!response.ok) throw new Error(response.status === 403 ? "denied" : "unavailable");
        const payload = await response.json();
        if (!this.current(root, generation)) return;
        if (!payload || payload.locale !== document.documentElement.lang ||
            !payload.messages || typeof payload.messages !== "object" ||
            messageKeys.some(key => typeof payload.messages[key] !== "string" ||
              payload.messages[key].length === 0 || payload.messages[key].length > 4096)) {
          throw new Error("locale");
        }
        this.words = Object.freeze(Object.fromEntries(messageKeys.map(key => [key, payload.messages[key]])));
        this.mount();
        this.initialized = true;
      } catch (error) {
        if (!this.current(root, generation)) return;
        // This fallback exists in every Admin surface, including non-Media
        // pages. Never silently substitute English for the selected locale.
        this.showError(error.message === "denied" ? "permission_denied" :
          error.message === "locale" ? "locale_unavailable" : "service_unavailable");
      } finally {
        if (this.catalogController === controller) this.catalogController = null;
      }
    }
    text(key) { return this.words[key]; }
    mount() {
      this.replaceChildren();
      const raw = this.getAttribute("accept");
      const types = raw ? raw.split(",").map(value => value.trim()) : [];
      this.accepts = !raw ? null : new Set(types.length <= 32 && types.every(type => mimePattern.test(type)) ? types : []);
      this.details = make("details");
      this.summary = make("summary", this.text("open"));
      this.details.append(this.summary);
      const controls = make("div");
      this.refresh = make("button", this.text("refresh"));
      this.refresh.type = "button";
      this.next = make("button", this.text("next"));
      this.next.type = "button";
      this.next.hidden = true;
      controls.append(this.refresh, this.next);
      this.list = make("ul");
      this.list.setAttribute("aria-label", this.text("results"));
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
      const mountedDetails = this.details;
      mountedDetails.addEventListener("toggle", () => {
        // The previous detached DOM may deliver a queued toggle event after
        // a route change. It must never operate on the new Picker instance.
        if (this.details !== mountedDetails || !mountedDetails.isConnected) return;
        if (mountedDetails.open) this.page("");
        else if (this.controller) this.controller.abort();
      });
      if (!this.escapeBound) {
        this.escapeBound = true;
        this.addEventListener("keydown", event => {
          if (event.key === "Escape" && this.details?.isConnected && this.details.open) {
            event.preventDefault();
            this.details.open = false;
            this.summary.focus();
          }
        });
      }
    }
    async request(path, root, generation) {
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
        if (this.controller !== controller || !this.current(root, generation) || !this.details.open) return null;
        return data;
      } finally {
        clearTimeout(timeout);
        if (this.controller === controller) this.controller = null;
      }
    }
    message(key) {
      this.notice.textContent = this.text(key);
      this.notice.setAttribute("role", key === "unavailable" || key === "denied" || key === "stale" ? "alert" : "status");
    }
    async page(cursor) {
      if (this.busy || !this.details.open) return;
      if (cursor && !cursorPattern.test(cursor)) { this.message("unavailable"); return; }
      const root = this.root, generation = this.generation;
      this.busy = true;
      this.refresh.disabled = true;
      this.next.disabled = true;
      this.message("loading");
      try {
        const data = await this.request("/page" + (cursor ? "/" + cursor : ""), root, generation);
        if (!data || !this.current(root, generation)) return;
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
          select.append(document.createTextNode(this.text("select") + " "));
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
        if (!this.current(root, generation)) return;
        if (error.name !== "AbortError") this.message(error.message === "denied" ? "denied" : "unavailable");
        this.list.replaceChildren();
        this.next.hidden = true;
      } finally {
        if (this.current(root, generation)) {
          this.busy = false;
          this.refresh.disabled = false;
          this.next.disabled = false;
        }
      }
    }
    async choose(id) {
      if (this.busy || !this.details.open || !idPattern.test(id)) return;
      const root = this.root, generation = this.generation;
      this.busy = true;
      this.message("loading");
      try {
        const data = await this.request("/asset/" + id, root, generation);
        if (!data || !this.current(root, generation)) return;
        const asset = data.asset;
        if (!valid(asset) || asset.id !== id || this.accepts && !this.accepts.has(asset.mime)) {
          this.message("stale");
          return;
        }
        // Metadata is only a choice, not a capability token or a product write.
        const selection = Object.freeze({id:asset.id, filename:asset.filename, mime:asset.mime,
          size:asset.size, width:asset.width, height:asset.height, revision:asset.revision});
        if (typeof data.message !== "string" || !data.message.length || data.message.length > 8192) {
          throw new Error("unavailable");
        }
        this.details.open = false;
        this.result.textContent = data.message;
        this.dispatchEvent(new CustomEvent("achrix:media-selected", {detail:selection, bubbles:true}));
        this.summary.focus();
      } catch (error) {
        if (this.current(root, generation) && error.name !== "AbortError")
          this.message(error.message === "denied" ? "denied" : error.message === "stale" ? "stale" : "unavailable");
      } finally {
        if (this.current(root, generation)) this.busy = false;
      }
    }
  }
  customElements.define("achrix-media-picker", MediaPicker);
})();
