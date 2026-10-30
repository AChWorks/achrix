// SPDX-License-Identifier: MPL-2.0
"use strict";
(() => {
 const text = (key, params) => window.AChrixI18n.text("media", key, params);
 const rows = document.getElementById("assets");
 if (!rows) return;
 const file = document.getElementById("media-file");
 const uploadResult = document.getElementById("upload-result");
 const statusResult = document.getElementById("asset-status");
 const statusID = document.getElementById("media-status-id");
 const idPattern = /^[A-Z2-7]{26}$/;
 function element(tag,content) { const node=document.createElement(tag); if (content !== undefined) node.textContent=content; return node; }
 function hidden(form,name,value) { const input=element("input");input.type="hidden";input.name=name;input.value=value;form.append(input); }
 function tableState() { const empty=rows.children.length===0;document.getElementById("media-empty").hidden=!empty;document.getElementById("media-table").hidden=empty; }
 function row(value) {
  const a=value.asset;
  const tr=element("tr");tr.dataset.assetId=a.id;
  const name=element("td");const filename=element("bdi",a.filename);filename.dir="auto";const id=element("bdi",a.id);id.dir="ltr";name.append(filename,element("br"),id);tr.append(name);
  const detail=element("td");const size=`${a.mime} · ${a.size} B`;const dimensions=element("bdi",a.width>0&&a.height>0?`${size} · ${a.width}×${a.height}`:size);dimensions.dir="ltr";detail.append(dimensions);tr.append(detail);
  const actions=element("td");
  if (value.permissions.read===true) {const link=element("a",text("download.action"));link.href="/admin/media/read/"+a.id;link.dataset.download="";actions.append(link);}
  if (value.permissions.delete===true) {
   const form=element("form");form.dataset.adminForm="";form.dataset.operation="delete";form.action="/admin/media/delete";form.method="post";
   hidden(form,"id",a.id);hidden(form,"revision",a.revision);
   const label=element("label");const confirm=element("input");confirm.type="checkbox";confirm.dataset.confirm="";confirm.required=true;label.append(confirm,document.createTextNode(text("delete.confirm")));form.append(label);
   const submit=element("button",text("delete.action"));submit.type="submit";submit.disabled=true;form.append(submit);actions.append(form);
  }
  if (value.permissions.read!==true && value.permissions.delete!==true) actions.append(element("p",text(value.warning==="permissions_unavailable"?"permissions.unavailable":"asset.denied")));
  tr.append(actions);return tr;
 }
 document.addEventListener("admin:success",event => {
  const operation=event.target.dataset.operation;
  if (operation==="list") {
   // The owning server returns one bounded page; never append an unbounded feed.
   rows.replaceChildren(...event.detail.assets.map(row));tableState();
   document.getElementById("media-next-cursor").value=event.detail.next_cursor;
   document.getElementById("media-next-page").hidden=!event.detail.next_cursor;
  } else if (operation==="upload" || operation==="status") {
   const a=event.detail.asset;statusID.value=a.id;
   const states = {pending:text("state.pending"),ready:text("state.ready"),deleting:text("state.deleting"),deleted:text("state.deleted")};
   const id=element("bdi",a.id);id.dir="ltr";
   const revision=element("bdi",a.revision);revision.dir="ltr";
   statusResult.replaceChildren(window.AChrixI18n.parts("media", "status.result", {ID:id, State:states[a.state], Revision:revision}));
   if (operation==="upload") {uploadResult.replaceChildren(...Array.from(statusResult.childNodes,node=>node.cloneNode(true)));file.value="";}
  } else if (operation==="delete") {
   const id=event.target.elements.id.value;
   for (const tr of rows.children) if (tr.dataset.assetId===id) {tr.remove();break;}
   tableState();
  }
 });
 document.addEventListener("admin:failure",event => {
  if (event.target.dataset.operation==="upload") {
   file.value="";
   // A previous upload ID cannot identify this newly failed/unknown operation.
   statusID.value=""; statusResult.textContent="";
   const id=event.detail.server && event.detail.server.asset_id;
   if (typeof id==="string" && idPattern.test(id)) statusID.value=id;
   if (event.detail.unknown) {
    uploadResult.textContent=text("upload.unknown");
   } else uploadResult.textContent="";
  }
  if (event.target.dataset.operation==="status") statusResult.textContent="";
 });
})();
