(() => {
 const notice = $('notice');
 let batch = [];
 const eligible = code => !code.claimedAt && !code.usedAt && !code.revokedAt && (!code.expiresAt || new Date(code.expiresAt)>new Date());
 function cell(row,value){const td=row.insertCell();td.textContent=value??'—';return td;}
 function date(value){return value?new Date(value).toLocaleString():'—';}
 function userLabel(user,id){return [user?.name,user?.email,id].filter(Boolean).join('\n')||'—';}
 function empty(id,cols){if(!$(id).children.length){const row=$(id).insertRow();const td=cell(row,'暂无记录');td.colSpan=cols;}}
 async function action(button,fn){button.disabled=true;notice.textContent='';try{await fn();await refresh();}catch(e){notice.textContent=e.message;}finally{button.disabled=false;}}
 async function claimAndCopy(button,codes){
  button.disabled=true;notice.textContent='';
  try{
   const data=await adminRequest('/v1/admin/invite-codes/claim',{method:'POST',body:JSON.stringify({codes})});
   batch=batch.filter(code=>!data.codes.includes(code));
   try{await navigator.clipboard.writeText(data.codes.join('\n'));notice.textContent='已领取并复制，不能再次复制';}
   catch{$('copyFallback').hidden=false;$('copyFallbackValue').value=[$('copyFallbackValue').value,data.codes.join('\n')].filter(Boolean).join('\n');notice.textContent='已领取，但浏览器未能写入剪贴板。请从下方领取结果手动复制，本次领取不会撤回。';}
  }catch(e){notice.textContent=e.message;}
  finally{try{await refresh();}catch(e){notice.textContent+=' 刷新失败，请重新刷新确认领取状态。';}}
 }
 async function refresh(){
  const [codes,agents]=await Promise.all([adminRequest('/v1/admin/invite-codes'),adminRequest('/v1/admin/web-agents')]);
  $('codeTotal').textContent=codes.total;$('codeUsed').textContent=codes.used;$('agentTotal').textContent=agents.total;$('agentRunning').textContent=agents.running;
  $('codes').replaceChildren();
  for(const code of codes.codes){
   const row=$('codes').insertRow();
   for(const value of [code.code,code.usedAt?'已兑换':code.revokedAt?'已撤销':code.expiresAt&&new Date(code.expiresAt)<=new Date()?'已过期':code.claimedAt?'已领取':'未领取',code.product||'通用',code.agentId||'—',userLabel(code.usedByUser,code.usedBy),date(code.usedAt),[date(code.expiresAt),code.note].filter(Boolean).join('\n')])cell(row,value);
   const td=cell(row,'');const copyButton=document.createElement('button');copyButton.textContent=code.usedAt?'已兑换':code.claimedAt?'已领取':'领取并复制';copyButton.disabled=!eligible(code);copyButton.onclick=()=>claimAndCopy(copyButton,[code.code]);td.append(copyButton);
   if(!code.usedAt&&!code.revokedAt){const b=document.createElement('button');b.textContent='撤销';b.onclick=()=>action(b,()=>adminRequest('/v1/admin/invite-codes/'+encodeURIComponent(code.code),{method:'DELETE'}));td.append(b);}
  }
  batch=batch.filter(code=>codes.codes.some(item=>item.code===code&&eligible(item)));
  $('generatedCodes').value=batch.join('\n');$('copyGenerated').disabled=batch.length===0;
  empty('codes',8);$('agents').replaceChildren();
  for(const a of agents.items){
   const row=$('agents').insertRow();for(const value of [[a.agentId,a.inviteCode].filter(Boolean).join('\n'),userLabel(a.user,a.userId),a.product,[a.status,a.phase].filter(Boolean).join(' / ')])cell(row,value);
   const resource=cell(row,'');if(a.podId){const link=document.createElement('a');link.href='/admin/hymatrix';link.textContent=a.podId;resource.append(link);}resource.append(document.createTextNode('\n'+(a.accessKeyId||'—')));
   cell(row,a.botUsername||'—');cell(row,a.error||'—');const td=cell(row,'');if(a.status==='failed'){const b=document.createElement('button');b.textContent='重试';b.onclick=()=>action(b,()=>adminRequest('/v1/admin/web-agents/'+encodeURIComponent(a.agentId)+'/retry',{method:'POST'}));td.append(b);}
  }
  empty('agents',8);
 }
 $('create').onsubmit=e=>{e.preventDefault();action(e.submitter,async()=>{const expiry=$('expiry').value;const data=await adminRequest('/v1/admin/invite-codes',{method:'POST',body:JSON.stringify({count:Number($('count').value),product:$('product').value.trim(),note:$('note').value,expiresAt:expiry?new Date(expiry).toISOString():null})});batch=data.codes.map(c=>c.code);$('generatedCodes').value=batch.join('\n');$('generated').hidden=false;notice.textContent=`已生成 ${data.codes.length} 个邀请码`;});};
 $('copyGenerated').onclick=e=>claimAndCopy(e.currentTarget,[...batch]);$('refresh').onclick=e=>action(e.currentTarget,async()=>{});refresh().catch(e=>notice.textContent=e.message);
})();
