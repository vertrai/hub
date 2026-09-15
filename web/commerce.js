(() => {
 const notice = $('notice');
 function cell(row,value){const td=row.insertCell();td.textContent=value??'—';return td;}
 function date(value){return value?new Date(value).toLocaleString():'—';}
 function userLabel(user,id){return [user?.name,user?.email,id].filter(Boolean).join('\n')||'—';}
 function empty(id,cols){if(!$(id).children.length){const row=$(id).insertRow();const td=cell(row,'暂无记录');td.colSpan=cols;}}
 async function action(button,fn){button.disabled=true;notice.textContent='';try{await fn();await refresh();}catch(e){notice.textContent=e.message;}finally{button.disabled=false;}}
 async function copy(value){try{await navigator.clipboard.writeText(value);notice.textContent='已复制';}catch{notice.textContent='无法访问剪贴板，请选中文本后手动复制';}}
 async function refresh(){
  const [codes,agents]=await Promise.all([adminRequest('/v1/admin/invite-codes'),adminRequest('/v1/admin/web-agents')]);
  $('codeTotal').textContent=codes.total;$('codeUsed').textContent=codes.used;$('agentTotal').textContent=agents.total;$('agentRunning').textContent=agents.running;
  $('codes').replaceChildren();
  for(const code of codes.codes){
   const row=$('codes').insertRow();
   for(const value of [code.code,code.usedAt?'已兑换':code.revokedAt?'已撤销':code.expiresAt&&new Date(code.expiresAt)<=new Date()?'已过期':'可用',code.product||'通用',code.agentId||'—',userLabel(code.usedByUser,code.usedBy),date(code.usedAt),[date(code.expiresAt),code.note].filter(Boolean).join('\n')])cell(row,value);
   const td=cell(row,'');const copyButton=document.createElement('button');copyButton.textContent='复制';copyButton.onclick=()=>copy(code.code);td.append(copyButton);
   if(!code.usedAt&&!code.revokedAt){const b=document.createElement('button');b.textContent='撤销';b.onclick=()=>action(b,()=>adminRequest('/v1/admin/invite-codes/'+encodeURIComponent(code.code),{method:'DELETE'}));td.append(b);}
  }
  empty('codes',8);$('agents').replaceChildren();
  for(const a of agents.items){
   const row=$('agents').insertRow();for(const value of [[a.agentId,a.inviteCode].filter(Boolean).join('\n'),userLabel(a.user,a.userId),a.product,[a.status,a.phase].filter(Boolean).join(' / ')])cell(row,value);
   const resource=cell(row,'');if(a.podId){const link=document.createElement('a');link.href='/admin/hymatrix';link.textContent=a.podId;resource.append(link);}resource.append(document.createTextNode('\n'+(a.accessKeyId||'—')));
   cell(row,a.botUsername||'—');cell(row,a.error||'—');const td=cell(row,'');if(a.status==='failed'){const b=document.createElement('button');b.textContent='重试';b.onclick=()=>action(b,()=>adminRequest('/v1/admin/web-agents/'+encodeURIComponent(a.agentId)+'/retry',{method:'POST'}));td.append(b);}
  }
  empty('agents',8);
 }
 $('create').onsubmit=e=>{e.preventDefault();action(e.submitter,async()=>{const expiry=$('expiry').value;const data=await adminRequest('/v1/admin/invite-codes',{method:'POST',body:JSON.stringify({count:Number($('count').value),product:$('product').value.trim(),note:$('note').value,expiresAt:expiry?new Date(expiry).toISOString():null})});$('generatedCodes').value=data.codes.map(c=>c.code).join('\n');$('generated').hidden=false;notice.textContent=`已生成 ${data.codes.length} 个邀请码`;});};
 $('copyGenerated').onclick=()=>copy($('generatedCodes').value);$('refresh').onclick=e=>action(e.currentTarget,async()=>{});refresh().catch(e=>notice.textContent=e.message);
})();
