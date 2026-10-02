// SPDX-License-Identifier: MPL-2.0
"use strict";
// Test-only Playwright runner. All data/credentials are public synthetic input.
// Product runtime has no Node/Playwright dependency.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const {chromium} = require(process.env.ACHRIX_PLAYWRIGHT_MODULE);
assert.notEqual(process.env.NODE_TLS_REJECT_UNAUTHORIZED,"0","TLS certificate verification must remain enabled");
const fixturePath = process.argv[2];
assert(fixturePath && process.argv.length === 3, "one private fixture metadata path required");
const root = path.dirname(fixturePath);
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function withDeadline(promise,ms,label) {
  let timer;
  try { return await Promise.race([promise,new Promise((_,reject)=>{timer=setTimeout(()=>reject(new Error(label)),ms)})]); }
  finally { clearTimeout(timer); }
}
let probeID = 0;
async function probe() {
  const id = ++probeID;
  await fs.writeFile(path.join(root, "probe.json.pending"), JSON.stringify({id}), {mode:0o600});
  await fs.rename(path.join(root, "probe.json.pending"), path.join(root, "probe.json"));
  for (let n=0; n<100; n++) {
    try { const value=JSON.parse(await fs.readFile(path.join(root,"probe-result.json"),"utf8")); if(value.id===id) return value; }
    catch(error) { if(error.code!=="ENOENT") throw error; }
    await delay(50);
  }
  throw new Error("private durable-state probe deadline");
}
function sameState(before,after,label) {
  for (const key of ["accounts","audit","readyAssets","deletedAssets","digest"]) assert.equal(after[key],before[key],label+": "+key);
}
async function main() {
  const metadata=JSON.parse(await fs.readFile(fixturePath,"utf8"));
  assert.deepEqual(metadata.endpoints.map(e=>e.language),["en","fa"]);
  const origins=new Set(metadata.endpoints.map(e=>e.origin));
  for (const e of metadata.endpoints) {
    assert.match(e.origin,/^https:\/\/127\.0\.0\.1:[0-9]+$/);
    assert.match(e.spki,/^[A-Za-z0-9+/]{43}=$/);
  }
  const browser=await chromium.launch({headless:true,args:[
    "--ignore-certificate-errors-spki-list="+[...new Set(metadata.endpoints.map(e=>e.spki))].join(","),
    "--disable-background-networking","--disable-component-update"
  ]});
  const reports=[];
  try {
    for (const endpoint of metadata.endpoints) {
      const fa=endpoint.language==="fa";
      const completed=fa?"انجام شد":"Completed";
      const unknown=fa?"نتیجه قابل تأیید نیست":"outcome could not be confirmed";
      const context=await browser.newContext({viewport:{width:1280,height:900},ignoreHTTPSErrors:false});
      await context.route("**/*",route=>{
        const u=new URL(route.request().url());
        return origins.has(u.origin)?route.continue():route.abort("blockedbyclient");
      });
      context.setDefaultTimeout(10000); context.setDefaultNavigationTimeout(20000);
      const page=await context.newPage();
      const consoleErrors=[];
      page.on("pageerror",error=>consoleErrors.push(error.message));
      const calls=[],csrfAdmissions=[];
      page.on("request",request=>{
        const pathname=new URL(request.url()).pathname;
        calls.push({method:request.method(),path:pathname});
        if(pathname==="/auth/csrf"&&request.method()==="GET") {
          // Observe actual browser-generated source headers without overriding
          // routing or logging cookie/CSRF/credential-bearing headers.
          csrfAdmissions.push(request.allHeaders().then(headers=>({referer:headers.referer,origin:headers.origin})));
        }
      });
      const cost=[];
      page.on("response",response=>{
        const type=response.request().resourceType();
        if(!["document","script","stylesheet"].includes(type)) return;
        const item={path:new URL(response.url()).pathname,bytes:0};
        cost.push(item);
        response.body().then(body=>{item.bytes=body.length}).catch(()=>{});
      });
      async function navigate(relative) {
        const response=await page.goto(endpoint.origin+relative);
        assert(response,"HTTP document response");
        const headers=response.headers();
        assert.equal(headers["cache-control"],"no-store");
        assert.equal(headers["x-content-type-options"],"nosniff");
        assert.equal(headers["referrer-policy"],"no-referrer");
        assert.equal(headers["x-frame-options"],"DENY");
        assert(headers["content-security-policy"].includes("frame-ancestors 'none'"));
        assert(!headers["content-security-policy"].includes("unsafe-inline"));
        return response;
      }
      async function responseSubmit(form,route,status=200) {
        const validation=await form.evaluate(f=>({valid:f.checkValidity(),invalid:[...f.elements].filter(e=>e.validity&&!e.validity.valid).map(e=>e.name)}));
        assert.equal(validation.valid,true,route+": native form validation: "+validation.invalid.join(","));
        const admissionBefore=csrfAdmissions.length;
        const pending=page.waitForResponse(r=>new URL(r.url()).pathname===route&&r.request().method()==="POST");
        await form.locator('button[type="submit"]').press("Enter");
        const response=await pending; assert.equal(response.status(),status,route);
        assert.equal(csrfAdmissions.length,admissionBefore+1,"one actual UI CSRF admission: "+route);
        const source=await csrfAdmissions[admissionBefore];
        assert.equal(source.referer,endpoint.origin+"/","UI CSRF sends only the same-origin root as Referer");
        if(source.origin!==undefined) assert.equal(source.origin,endpoint.origin,"UI CSRF Origin remains exact");
        await page.waitForFunction(()=>document.querySelector('form[aria-busy="true"]')===null);
        if(status===200||status===204) {
          assert((await page.locator("#status").innerText()).includes(completed));
          assert.equal(await page.locator("#status").evaluate(e=>e===document.activeElement),true,"completion announced and focused");
        }
        return response;
      }
      async function signIn(login) {
        await navigate("/admin/login");
        await page.locator("#login").fill(login);
        await page.locator("#password").fill(metadata.password);
        await page.locator("form[data-login] button").press("Enter");
        await page.waitForURL(endpoint.origin+"/admin/");
      }
      async function request(relative,body,csrf,extra={}) {
        return page.evaluate(async ({relative,body,csrf,extra})=>{
          const headers={"Content-Type":"application/json","X-Identity-Request":"1",...extra};
          if(csrf!==null) headers["X-CSRF-Token"]=csrf;
          const response=await fetch(relative,{method:"POST",credentials:"same-origin",headers,body:JSON.stringify(body),cache:"no-store"});
          return {status:response.status,text:await response.text()};
        },{relative,body,csrf,extra});
      }
      async function csrf() {
        return page.evaluate(async ()=>{
          const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),10000);
          try {
            const response=await fetch("/auth/csrf",{credentials:"same-origin",headers:{"X-Identity-Request":"1"},cache:"no-store",
              mode:"same-origin",redirect:"error",referrer:location.origin+"/",referrerPolicy:"same-origin",signal:controller.signal});
            if(!response.ok) throw new Error("fixture csrf unavailable");
            return (await response.json()).csrf;
          } finally { clearTimeout(timer); }
        });
      }

      await navigate("/admin");
      assert.equal(new URL(page.url()).pathname,"/admin/login","anonymous redirect");
      assert.equal(await page.locator("html").getAttribute("lang"),endpoint.language);
      assert.equal(await page.locator("html").getAttribute("dir"),fa?"rtl":"ltr");
      // The first Tab reaches the skip link; Enter puts actual keyboard focus on main.
      await page.keyboard.press("Tab");
      assert.equal(await page.locator(".skip").evaluate(e=>e===document.activeElement),true);
      await page.keyboard.press("Enter");
      assert.equal(await page.locator("#main").evaluate(e=>e===document.activeElement),true);
      assert.equal(await page.locator("#login").getAttribute("autocomplete"),"username");
      assert.equal(await page.locator("#password").getAttribute("autocomplete"),"current-password");
      await signIn(metadata.login);
      const cookie=(await context.cookies()).find(c=>c.name==="__Host-AChrix-Session");
      assert(cookie&&cookie.secure&&cookie.httpOnly&&cookie.sameSite==="Strict"&&cookie.path==="/","Identity hardened cookie");
      assert.equal(await page.evaluate(()=>document.cookie),"");
      assert.equal(await page.evaluate(()=>localStorage.length+sessionStorage.length),0,"no browser storage credentials/tokens");
      assert.equal(await page.locator('nav a[href="/admin/identity"]').count(),1);
      assert.equal(await page.locator('nav a[href="/admin/media"]').count(),1);

      await navigate("/admin/identity");
      assert.equal(await page.locator("#account-empty").isVisible(),true);
      assert.equal(await page.locator("nav").evaluate(e=>getComputedStyle(e).direction),fa?"rtl":"ltr");
      const navBox=await page.locator("nav").boundingBox(), mainBox=await page.locator("main").boundingBox();
      assert(fa?navBox.x>mainBox.x:navBox.x<mainBox.x,"desktop navigation follows direction");
      await page.setViewportSize({width:375,height:812});
      assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),"responsive page has no viewport overflow");
      assert((await page.locator("nav").boundingBox()).y<(await page.locator("main").boundingBox()).y,"mobile navigation precedes content");
      await page.setViewportSize({width:1280,height:900});
      await page.locator("#new-login").focus(); await page.keyboard.press("Tab");
      assert.equal(await page.locator("#new-password").evaluate(e=>e===document.activeElement),true,"form keyboard order");
      assert.equal(await page.locator("input:not([type=hidden]):not([type=checkbox])").evaluateAll(inputs=>inputs.every(input=>input.labels.length>0)),true,"visible inputs have labels");
      const create=page.locator('form[data-operation="create"]');
      const login="browser-created-"+endpoint.language;
      await page.locator("[pattern]").evaluateAll(inputs=>inputs.forEach(input=>new RegExp(input.pattern,"v")));
      await page.locator("#new-password").fill("🔐".repeat(15));
      const beforeInvalidLogin=calls.length;
      for(const selector of ["#new-login","#lookup-login"]) {
        const input=page.locator(selector);
        await input.fill("invalid login");
        assert.equal(await input.evaluate(e=>e.validity.patternMismatch),true,"native login syntax rejects spaces: "+selector);
        await input.locator("xpath=..").locator('button[type="submit"]').press("Enter");
        assert.equal(await input.evaluate(e=>e===document.activeElement),true,"native validation focuses the invalid login");
      }
      await delay(100);
      assert.equal(calls.length,beforeInvalidLogin,"invalid login submits no request");
      await page.locator("#lookup-login").fill(login);
      await page.locator("#new-login").fill(login);
      const startCalls=calls.length;
      const created=await responseSubmit(create,"/admin/identity/create");
      const createdBody=await created.json();
      const id=createdBody.account.id;
      assert.match(id,/^[A-Z2-7]{26}$/);
      assert.equal(await page.locator("#account-id").innerText(),id);
      assert.equal(await page.locator("#account-login").innerText(),login);
      assert.equal(await page.locator("#new-password").inputValue(),"");
      assert.equal(calls.slice(startCalls).filter(c=>c.path==="/auth/csrf").length,1);
      assert.equal(calls.slice(startCalls).filter(c=>c.path==="/admin/identity/create").length,1,"one create, no replay");
      assert.equal(await page.locator("#account-id").evaluate(e=>e.tagName),"BDI");

      await page.locator("#lookup-id").fill(id);
      await responseSubmit(page.locator('form[data-operation="lookup"]'),"/admin/identity/lookup");
      const oldRevision=await page.locator("#account-revision").innerText();
      assert.match(oldRevision,/^[1-9][0-9]*$/);
      const passwordForm=page.locator('form[data-operation="password"]');
      await page.locator("#replacement-password").fill("Synthetic replacement password 123!");
      await passwordForm.locator("[data-confirm]").check();
      await responseSubmit(passwordForm,"/admin/identity/password",204);
      assert.equal(await page.locator("#replacement-password").inputValue(),"");
      assert.equal(await page.locator("#account").isVisible(),false,"stale metadata cleared after mutation");
      await responseSubmit(page.locator('form[data-operation="lookup"]'),"/admin/identity/lookup");
      const revision=await page.locator("#account-revision").innerText();
      assert(BigInt(revision)>BigInt(oldRevision));
      const token=await csrf();
      const beforeStale=await probe();
      const stale=await request("/admin/identity/enabled",{id,revision:oldRevision,enabled:false},token);
      assert.equal(stale.status,409,"revision precondition protects status");
      sameState(beforeStale,await probe(),"stale mutation has no durable effects");
      const enabledForm=page.locator('form[data-operation="enabled"]');
      await enabledForm.locator('input[name="enabled"]').uncheck();
      await enabledForm.locator("[data-confirm]").check();
      await responseSubmit(enabledForm,"/admin/identity/enabled",204);
      await responseSubmit(page.locator('form[data-operation="lookup"]'),"/admin/identity/lookup");
      assert.equal(await page.locator("#account-enabled").innerText(),fa?"غیرفعال":"Disabled");
      const revokeForm=page.locator('form[data-operation="revoke"]');
      await revokeForm.locator("[data-confirm]").check();
      await responseSubmit(revokeForm,"/admin/identity/revoke",204);

      // Fetch performs the real server mutation, then delivery is deliberately
      // aborted. This proves an unknown outcome, rather than a synthetic error
      // before commit. Neither the UI nor the test automatically replays it.
      const lostLogin="browser-lost-"+endpoint.language;
      await page.locator("#new-login").fill(lostLogin);
      await page.locator("#new-password").fill("Synthetic lost response password 123!");
      const beforeUnknown=await probe();
      let mutations=0, release, committed;
      const serverCommitted=new Promise(resolve=>{committed=resolve});
      const gate=new Promise(resolve=>{release=resolve});
      await page.route("**/admin/identity/create",async route=>{
        assert.equal(new URL(route.request().url()).origin,endpoint.origin);
        mutations++;
        const response=await route.fetch({timeout:10000,maxRedirects:0,maxRetries:0});
        assert.equal(response.status(),200,"lost acknowledgement server commit");
        committed();
        await gate;
        await route.abort("failed");
      });
      const unknownSubmitted=page.waitForResponse(r=>new URL(r.url()).pathname==="/auth/csrf");
      await create.locator('button[type="submit"]').click();
      await unknownSubmitted;
      await page.waitForFunction(()=>document.querySelector('form[data-operation="create"]').getAttribute("aria-busy")==="true");
      assert.equal(await create.locator("button").isDisabled(),true,"busy admits no duplicate action");
      await page.waitForFunction(()=>document.querySelector('#status').textContent.includes(document.documentElement.lang==='fa'?'صبر':'wait'));
      await withDeadline(serverCommitted,12000,"lost-ack server commit deadline");
      assert.equal((await probe()).accounts,beforeUnknown.accounts+1,"server committed before delivery abort");
      release();
      await page.waitForFunction(text=>document.querySelector("#status").textContent.includes(text),unknown);
      await page.unroute("**/admin/identity/create");
      assert.equal(mutations,1);
      assert.equal(await page.locator("#new-password").inputValue(),"");
      assert.equal(await page.locator("#new-login").inputValue(),lostLogin);
      assert.equal(await page.locator("#lookup-login").inputValue(),lostLogin,"unknown create preserves exact reconciliation identity");
      const recovered=await responseSubmit(page.locator('form[data-operation="lookup-login"]'),"/admin/identity/lookup-login");
      assert.equal((await recovered.json()).account.login,lostLogin);
      assert.equal(await page.locator("#account-login").innerText(),lostLogin);
      const afterUnknown=await probe();
      assert.equal(afterUnknown.accounts,beforeUnknown.accounts+1);
      assert.equal(afterUnknown.audit,beforeUnknown.audit+1,"exactly one accountable create");
      await page.locator("#lookup-login").fill("browser-missing-"+endpoint.language);
      await responseSubmit(page.locator('form[data-operation="lookup-login"]'),"/admin/identity/lookup-login",404);
      assert(!(await page.locator("#status").innerText()).includes(unknown),"known read-only not-found is not mutation uncertainty");
      if(!fa) {
        // Hold an already-submitted read before sending it to the server. The
        // actual client deadline must restore usable UI without an infinite busy
        // state or automatic resend. Known read-only failures remain distinct.
        let readRequests=0;
        await page.route("**/admin/identity/lookup-login",async route=>{
          readRequests++; await delay(16500);
          try { await route.abort("timedout"); } catch { /* already aborted by client */ }
        });
        await page.locator("#lookup-login").fill(lostLogin);
        await page.locator('form[data-operation="lookup-login"] button').click();
        await page.waitForFunction(()=>document.querySelector('form[data-operation="lookup-login"]').getAttribute("aria-busy")==="true");
        await page.waitForFunction(()=>document.querySelector('form[aria-busy="true"]')===null,{},{timeout:17000});
        assert.equal(readRequests,1,"finite read wait never retries");
        assert(!(await page.locator("#status").innerText()).includes(unknown),"interrupted query is not an unknown mutation");
        await page.unroute("**/admin/identity/lookup-login");
      }

      // Both actual owning surfaces use the normally resolved SDK and real data.
      const retained=await mediaFlow({page,navigate,responseSubmit,request,csrf,probe,endpoint,fa,context,images:metadata.images});

      // Missing CSRF and crafted privileged calls are checked against the real
      // cookie-bound handler, then independent persisted state is compared.
      await navigate("/admin/identity");
      const beforeCSRF=await probe();
      assert.equal((await request("/admin/identity/create",{login:"csrf-denied-"+endpoint.language,password:metadata.password},null)).status,401);
      sameState(beforeCSRF,await probe(),"missing CSRF has no effects");
      await page.locator("#logout").click();
      await page.waitForURL(endpoint.origin+"/admin/login");
      await signIn(metadata.viewerLogin);
      await navigate("/admin/identity");
      assert.equal(await page.locator('form[data-operation="create"]').count(),0,"presentation reflects viewer permission");
      const viewerToken=await csrf();
      const beforeDenied=await probe();
      assert.equal((await request("/admin/identity/create",{login:"viewer-denied-"+endpoint.language,password:metadata.password},viewerToken)).status,403);
      assert.equal((await request("/admin/identity/lookup-login",{login:metadata.login},viewerToken)).status,403,"AccountRead never grants exact-login discovery");
      assert.equal((await request("/admin/identity/enabled",{id,revision,enabled:true},viewerToken)).status,403);
      sameState(beforeDenied,await probe(),"domain permission denial has no durable effects");
      await navigate("/admin/media");
      const viewerRow=page.locator('#assets tr[data-asset-id="'+retained.id+'"]');
      assert.equal(await viewerRow.count(),1,"metadata viewer sees ready image");
      assert.equal(await viewerRow.locator("a[data-download],form[data-operation=delete]").count(),0,"metadata never grants Read or Delete");
      assert.equal(await page.locator("form[data-operation=upload]").count(),0);
      const beforeMediaDenied=await probe();
      assert.equal((await request("/admin/media/status",{id:retained.id},viewerToken)).status,403);
      assert.equal((await request("/admin/media/delete",{id:retained.id,revision:retained.revision},viewerToken)).status,403);
      const deniedRead=await page.evaluate(async id=>{const r=await fetch("/admin/media/read/"+id,{credentials:"same-origin",cache:"no-store"});return{status:r.status,type:r.headers.get("content-type"),text:await r.text()}},retained.id);
      assert.equal(deniedRead.status,403); assert.match(deniedRead.type,/application\/json/);
      assert(!deniedRead.text.includes(retained.filename),"denial leaks no metadata/bytes");
      sameState(beforeMediaDenied,await probe(),"denied exact Media operations leave ready bytes and state unchanged");
      await page.locator("#logout").click(); await page.waitForURL(endpoint.origin+"/admin/login");
      await signIn(metadata.deniedLogin);
      assert.equal(await page.locator("nav a").count(),0,"authenticated principal has no implicit grants");
      assert.equal((await navigate("/admin/identity")).status(),403);
      assert.equal((await navigate("/admin/media")).status(),403);
      // Return through the ordinary authenticated product flow for final cleanup.
      await signIn(metadata.login); await navigate("/admin/media");
      const retainedRow=page.locator('#assets tr[data-asset-id="'+retained.id+'"]');
      const deleteRetained=retainedRow.locator('form[data-operation="delete"]');
      await deleteRetained.locator("[data-confirm]").check();
      await responseSubmit(deleteRetained,"/admin/media/delete",204);
      assert.equal(await page.locator("#media-empty").isVisible(),true);
      assert.equal((await probe()).readyAssets,0,"each language leaves no ready image behind");

      assert.deepEqual(consoleErrors,[],"no uncaught browser exceptions");
      // Cold UI documents/assets are the payload that drives rendering. No
      // downloaded framework, web font or external runtime is permitted.
      await delay(100);
      assert(cost.every(item=>item.bytes<128*1024),"bounded per-response UI payload");
      const unique=new Map();
      for(const item of cost) unique.set(item.path,Math.max(unique.get(item.path)||0,item.bytes));
      const total=[...unique.values()].reduce((a,b)=>a+b,0);
      assert(total<128*1024,"bounded unique UI payload");
      reports.push({language:endpoint.language,browser:browser.version(),uniqueUIPayloadBytes:total,
        requests:calls.length,createRoundTrips:2,actualTLS:true,keyboard:true,unknownCreateRecovered:true,
        deniedDurableStateUnchanged:true});
      await context.close();
    }
    console.log(JSON.stringify({proof:"actual HTTPS normal-SDK Identity Media Admin browser",reports},null,2));
  } finally { await browser.close(); }
}
async function mediaFlow({page,navigate,responseSubmit,request,csrf,probe,endpoint,fa,context,images}) {
  await navigate("/admin/media");
  assert.equal(await page.locator("#media-empty").isVisible(),true,"real empty library");
  const png=Buffer.from(images.png,"base64"),jpeg=Buffer.from(images.jpeg,"base64");
  assert(png.length>0&&jpeg.length>0,"fixture-generated real image bytes");
  const pngName="تصویر‌نمونه-RTL-v1-"+endpoint.language+".png";
  const jpegName="<img src=x onerror=alert(1)>-"+endpoint.language+".jpg";
  const upload=page.locator('form[data-operation="upload"]');
  await page.locator("#media-file").setInputFiles({name:pngName,mimeType:"image/png",buffer:png});
  const uploaded=await responseSubmit(upload,"/admin/media/upload");
  const first=(await uploaded.json()).asset;
  assert.equal(first.filename,pngName);
  assert.equal(first.mime,"image/png");
  assert.equal(first.state,"ready");
  assert.equal(typeof first.revision,"string","int64 media revision stays decimal text");
  assert.match(first.revision,/^[1-9][0-9]*$/);
  assert.equal(await page.locator("#media-file").inputValue(),"","successful upload clears file selection");
  assert.equal(await page.locator("#media-status-id").inputValue(),first.id,"known durable ID retained");
  assert((await page.locator("#upload-result").innerText()).includes(first.id));
  const listed=await responseSubmit(page.locator("#media-refresh"),"/admin/media/list");
  assert.equal((await listed.json()).assets.length,1);
  let row=page.locator('#assets tr[data-asset-id="'+first.id+'"]');
  assert.equal(await row.locator("bdi").first().innerText(),pngName,"stored Persian ZWNJ/mixed text unchanged");
  assert.equal(await row.locator("bdi").first().getAttribute("dir"),"auto");
  assert.equal(await row.locator("bdi").nth(1).getAttribute("dir"),"ltr");
  assert.equal(await page.locator("#media-empty").isVisible(),false);
  assert.equal(await page.locator("#media-next-page").isVisible(),false,"bounded single page has no false next link");

  await page.locator("#media-file").setInputFiles({name:jpegName,mimeType:"image/jpeg",buffer:jpeg});
  const second=(await (await responseSubmit(upload,"/admin/media/upload")).json()).asset;
  assert.equal(second.mime,"image/jpeg");
  assert.equal(second.filename,jpegName);
  await responseSubmit(page.locator("#media-refresh"),"/admin/media/list");
  assert.equal(await page.locator("#assets tr").count(),2);
  const jpegRow=page.locator('#assets tr[data-asset-id="'+second.id+'"]');
  assert.equal(await jpegRow.locator("bdi").first().innerText(),jpegName,"stored markup-like filename rendered as text");
  assert.equal(await jpegRow.locator("img,svg,[onerror]").count(),0,"module-owned refreshed DOM uses contextual escaping");
  await page.setViewportSize({width:375,height:812});
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),"responsive image library does not overflow viewport");
  await page.setViewportSize({width:1280,height:900});

  // Exercise the real private attachment link using the authenticated browser.
  row=page.locator('#assets tr[data-asset-id="'+first.id+'"]');
  const downloadEvent=page.waitForEvent("download");
  await row.locator("a[data-download]").press("Enter");
  const download=await downloadEvent;
  assert.equal(await download.failure(),null);
  assert.equal(download.suggestedFilename(),pngName);
  assert.deepEqual(await fs.readFile(await download.path()),png,"actual browser attachment retains original bytes");
  const attachment=await page.evaluate(async id=>{
    const response=await fetch("/admin/media/read/"+id,{credentials:"same-origin",cache:"no-store"});
    return {status:response.status,mime:response.headers.get("content-type"),
      disposition:response.headers.get("content-disposition"),cache:response.headers.get("cache-control"),
      nosniff:response.headers.get("x-content-type-options"),size:(await response.arrayBuffer()).byteLength};
  },first.id);
  assert.equal(attachment.status,200); assert.equal(attachment.mime,"image/png");
  assert.match(attachment.disposition,/^attachment;/);
  assert.equal(attachment.cache,"private, no-store"); assert.equal(attachment.nosniff,"nosniff");
  assert.equal(attachment.size,png.length);

  const deleteForm=row.locator('form[data-operation="delete"]');
  assert.equal(await deleteForm.locator('input[name="revision"]').inputValue(),first.revision);
  const beforeConfirmation=await probe();
  await deleteForm.locator("button").press("Enter");
  assert.equal(await deleteForm.locator("[data-confirm]").evaluate(e=>e.validity.valueMissing),true);
  sameState(beforeConfirmation,await probe(),"unconfirmed consequential action is not submitted");

  const token=await csrf(),beforeInvalid=await probe();
  assert.equal((await request("/admin/media/delete",{id:first.id,revision:(BigInt(first.revision)+1n).toString()},token)).status,409);
  assert.equal((await request("/admin/media/delete",{id:first.id,revision:1},token)).status,400,"numeric JSON revision rejected");
  assert.equal((await request("/admin/media/delete",{id:first.id,revision:"9223372036854775808"},token)).status,400,"int64 overflow rejected before mutation");
  sameState(beforeInvalid,await probe(),"invalid or stale deletion leaves persisted state/files unchanged");
  await deleteForm.locator("[data-confirm]").check();
  await responseSubmit(deleteForm,"/admin/media/delete",204);
  assert.equal(await row.count(),0,"dynamic delegated form removes deleted row");
  assert.equal(await page.locator("#assets tr").count(),1);
  await page.locator("#media-status-id").fill(first.id);
  const status=(await (await responseSubmit(page.locator('form[data-operation="status"]'),"/admin/media/status")).json()).asset;
  assert.equal(status.state,"deleted","known ID reconciles its durable terminal state");
  const deletedRead=await page.evaluate(async id=>{
    const r=await fetch("/admin/media/read/"+id,{credentials:"same-origin",cache:"no-store"});
    return{status:r.status,type:r.headers.get("content-type"),disposition:r.headers.get("content-disposition")};
  },first.id);
  assert.equal(deletedRead.status,409); assert.match(deletedRead.type,/application\/json/);
  assert.equal(deletedRead.disposition,null,"failed read is never advertised as image attachment");

  // Invalid multipart framing is rejected before durable Media intent.
  const malformedToken=await csrf(),beforeMalformed=await probe();
  const malformed=await page.evaluate(async ({data,token})=>{
    const bytes=Uint8Array.from(atob(data),c=>c.charCodeAt(0));
    const body=new FormData(); body.append("file",new Blob([bytes],{type:"image/png"}),"extra-field.png"); body.append("extra","not permitted");
    const response=await fetch("/admin/media/upload",{method:"POST",credentials:"same-origin",
      headers:{"X-Identity-Request":"1","X-CSRF-Token":token},body,cache:"no-store"});
    return response.status;
  },{data:images.png,token:malformedToken});
  assert.equal(malformed,400);
  sameState(beforeMalformed,await probe(),"malformed multipart framing creates no intent/file");
  return second; // Retained ready bytes are used for the viewer denial proof.
}
main().catch(error=>{ console.error(error.stack||error.message); process.exitCode=1; });
