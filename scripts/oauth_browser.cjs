// SPDX-License-Identifier: MPL-2.0
"use strict";
// Standalone synthetic Playwright acceptance of product-composed OAuth. This
// never talks to a production issuer or emits cookie/token/authorization codes.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const {chromium} = require(process.env.ACHRIX_PLAYWRIGHT_MODULE);
assert.notEqual(process.env.NODE_TLS_REJECT_UNAUTHORIZED, "0", "TLS verification cannot be disabled");
const fixturePath = process.argv[2];
assert(fixturePath && process.argv.length === 3, "one owned fixture path required");

async function main() {
  const fixture = JSON.parse(await fs.readFile(fixturePath, "utf8"));
  assert.match(fixture.issuer, /^https:\/\/127\.0\.0\.1:[0-9]+$/);
  assert.match(fixture.client, /^https:\/\/localhost:[0-9]+$/);
  assert.equal(fixture.spkis.length, 3);
  for (const pin of fixture.spkis) assert.match(pin, /^[A-Za-z0-9+/]{43}=$/);
  const origins = new Set([fixture.issuer, fixture.client]);
  const browser = await chromium.launch({headless: true, args: [
    "--ignore-certificate-errors-spki-list=" + fixture.spkis.join(","),
    "--disable-background-networking", "--disable-component-update"
  ]});
  try {
    assert.equal(browser.version(), "153.0.8010.12", "pinned Chromium profile");
    for (const language of ["en-US", "fa-IR"]) {
      const fa = language.startsWith("fa");
      const ctx = await browser.newContext({locale: language, ignoreHTTPSErrors: false});
      try {
        await ctx.route("**/*", route => {
          const parsed = new URL(route.request().url());
          return origins.has(parsed.origin) ? route.continue() : route.abort("blockedbyclient");
        });
        ctx.setDefaultTimeout(12000);
        ctx.setDefaultNavigationTimeout(20000);
        const page = await ctx.newPage();
        const scriptErrors = [];
        page.on("pageerror", error => scriptErrors.push(error.message));
        const seen = [];
        page.on("response", response => {
          const parsed = new URL(response.url());
          seen.push({origin: parsed.origin, path: parsed.pathname, status: response.status()});
        });
        const start = await page.goto(fixture.client + "/start");
        assert.equal(start.status(), 200);
        assert.equal(await page.locator("#connect").count(), 1);
        await page.locator("#connect").click();
        await page.waitForURL(u => u.origin === fixture.issuer && u.pathname === "/login");
        assert.equal((await ctx.cookies(fixture.issuer)).filter(c => c.name === "__Host-AChrix-Session").length, 0);
        assert.equal(await page.locator("html").getAttribute("lang"), fa ? "fa" : "en");
        assert.equal(await page.locator("html").getAttribute("dir"), fa ? "rtl" : "ltr");
        assert.equal(await page.locator("#login").getAttribute("autocomplete"), "username");
        assert.equal(await page.locator("#password").getAttribute("autocomplete"), "current-password");
        await page.locator("#login").fill(fixture.login);
        await page.locator("#password").fill("wrong synthetic password");
        const rejected = page.waitForResponse(r => new URL(r.url()).pathname === "/auth/login" && r.request().method() === "POST");
        await page.locator('button[type="submit"]').click();
        assert.equal((await rejected).status(), 401, "real Identity rejects wrong password");
        assert.equal(await page.locator("#message").innerText(), "Login rejected");
        assert.equal((await ctx.cookies(fixture.issuer)).filter(c => c.name === "__Host-AChrix-Session").length, 0);
        await page.locator("#password").fill(fixture.password);
        await page.locator('button[type="submit"]').click();
        await page.waitForURL(u => u.origin === fixture.issuer && u.pathname === "/authorize");
        assert.equal(await page.locator("html").getAttribute("lang"), fa ? "fa" : "en");
        assert.equal(await page.locator("html").getAttribute("dir"), fa ? "rtl" : "ltr");
        assert.equal(await page.locator("h1").innerText(), fa ? "تأیید دسترسی" : "Authorize access");
        assert((await page.locator("main p").innerText()).includes("Browser & Notes Client"));
        assert((await page.locator("main p").innerText()).includes("notes.read"));
        assert.equal(await page.locator("button[name=decision]").count(), 2);
        const cookies = (await ctx.cookies(fixture.issuer)).filter(c => c.name === "__Host-AChrix-Session");
        assert.equal(cookies.length, 1);
        assert.equal(cookies[0].httpOnly, true);
        assert.equal(cookies[0].secure, true);
        assert.equal(cookies[0].sameSite, "Strict");
        assert.equal(cookies[0].path, "/");
        assert.equal(await page.evaluate(() => document.cookie), "", "session cannot be read by script");
        assert.equal(await page.evaluate(() => localStorage.length + sessionStorage.length), 0);
        const consent = page.waitForResponse(r => new URL(r.url()).pathname === "/authorize" && r.request().method() === "POST");
        await page.locator('button[name=decision][value="allow"]').click();
        const grantResult = await consent;
        if (grantResult.status() !== 303) {
          const headers = await grantResult.request().allHeaders();
          const outward = (await grantResult.text()).trim().slice(0, 140);
          throw new Error(`consent returned ${grantResult.status()}, publicReason=${JSON.stringify(outward)}, originIsExpected=${headers.origin === fixture.issuer}, sessionCookieSent=${Boolean(headers.cookie && headers.cookie.includes("__Host-AChrix-Session="))}`);
        }
        await page.waitForURL(u => u.origin === fixture.client && u.pathname === "/callback");
        assert.equal(await page.locator("#outcome").innerText(), "Bridge authorized");
        assert.equal(await page.locator("#account").innerText(), fixture.login);
        assert.equal(await page.evaluate(() => localStorage.length + sessionStorage.length), 0);
        assert.equal(await page.evaluate(() => document.cookie), "");
        assert(seen.some(v => v.origin === fixture.issuer && v.path === "/auth/login" && v.status === 200));
        assert(seen.some(v => v.origin === fixture.issuer && v.path === "/authorize" && v.status === 303));
        assert.equal(scriptErrors.length, 0, "browser runtime exceptions");
        console.log(`OAuth HTTPS Chromium ${language} product-login/consent/Bridge PASS`);
      } finally {
        await ctx.close();
      }
    }
  } finally {
    await browser.close();
  }
}
main().catch(error => {console.error(error.stack || String(error)); process.exitCode = 1;});
