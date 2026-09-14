(() => {
  const notice = document.getElementById('notice');
  function cell(row, value) { const td = row.insertCell(); td.textContent = value ?? ''; return td; }
  function date(value) { return value ? new Date(value).toLocaleString() : '—'; }
  async function action(button, fn) { button.disabled = true; notice.textContent = ''; try { await fn(); await refresh(); } catch (e) { notice.textContent = e.message; } finally { button.disabled = false; } }
  async function refresh() {
    const [codes,bills,agents] = await Promise.all([adminRequest('/v1/admin/invite-codes'),adminRequest('/v1/admin/billing'),adminRequest('/v1/admin/web-agents')]);
    document.getElementById('codes').replaceChildren();
    for (const code of codes.codes) {
      const row = document.getElementById('codes').insertRow();
      for (const value of [code.code,code.product || '通用',code.note,date(code.expiresAt),code.usedBy,code.usedAt ? '已兑换' : code.revokedAt ? '已撤销' : code.expiresAt && new Date(code.expiresAt) <= new Date() ? '已过期' : '可用']) cell(row,value);
      const td = cell(row,''); if (!code.usedAt && !code.revokedAt) { const b=document.createElement('button');b.textContent='撤销';b.onclick=()=>action(b,()=>adminRequest('/v1/admin/invite-codes/'+encodeURIComponent(code.code),{method:'DELETE'}));td.append(b); }
    }
    document.getElementById('billing').replaceChildren();
    for (const b of bills.items) { const row=document.getElementById('billing').insertRow(); for (const value of [b.id,b.product,b.status,b.subscriptionId,b.agentId,date(b.currentPeriodEnd)])cell(row,value); }
    document.getElementById('agents').replaceChildren();
    for (const a of agents.items) { const row=document.getElementById('agents').insertRow();for(const value of [a.agentId,a.userId,a.product,a.status,a.phase,a.error])cell(row,value);const td=cell(row,'');if(a.status==='failed'){const b=document.createElement('button');b.textContent='重试';b.onclick=()=>action(b,()=>adminRequest('/v1/admin/web-agents/'+encodeURIComponent(a.agentId)+'/retry',{method:'POST'}));td.append(b);} }
  }
  document.getElementById('create').onsubmit=e=>{e.preventDefault();const b=e.submitter;action(b,async()=>{const expiry=document.getElementById('expiry').value;await adminRequest('/v1/admin/invite-codes',{method:'POST',body:JSON.stringify({count:Number(document.getElementById('count').value),product:document.getElementById('product').value.trim(),note:document.getElementById('note').value,expiresAt:expiry?new Date(expiry).toISOString():null})});notice.textContent='邀请码已创建';});};
  document.getElementById('refresh').onclick=e=>action(e.currentTarget,async()=>{});
  refresh().catch(e=>notice.textContent=e.message);
})();
