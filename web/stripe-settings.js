(() => {
 const bools=['enabled','managedPayments','requireTermsOfServiceConsent','stopAgentOnPaymentFailure'];
 const texts=['successURL','cancelURL','portalReturnURL'];
 function render(data){for(const id of bools)$(id).checked=!!data[id];for(const id of texts)$(id).value=data[id]||'';$('secretKey').value='';$('webhookSecret').value='';$('secretState').textContent=data.secretKeyConfigured?'已配置；留空保持不变':'尚未配置';$('webhookState').textContent=data.webhookSecretConfigured?'已配置；留空保持不变':'尚未配置';}
 async function refresh(){
  $('settingsFields').disabled=true;
  const data=await adminRequest('/v1/admin/stripe/settings');render(data);$('settingsFields').disabled=false;
  const bills=await adminRequest('/v1/admin/billing');$('billing').replaceChildren();
  for(const b of bills.items){const row=$('billing').insertRow();for(const value of [b.id,b.product,b.status,b.subscriptionId,b.agentId,b.currentPeriodEnd&&!b.currentPeriodEnd.startsWith('0001')?new Date(b.currentPeriodEnd).toLocaleString():'—']){row.insertCell().textContent=value||'—';}}
  if(!bills.items.length){const td=$('billing').insertRow().insertCell();td.colSpan=6;td.textContent='暂无订阅账单';}
 }
 $('settings').onsubmit=async e=>{e.preventDefault();const data={};for(const id of bools)data[id]=$(id).checked;for(const id of [...texts,'secretKey','webhookSecret'])data[id]=$(id).value.trim();$('settingsFields').disabled=true;$('notice').textContent='';try{render(await adminRequest('/v1/admin/stripe/settings',{method:'PUT',body:JSON.stringify(data)}));$('notice').textContent='Stripe 设置已保存并生效';}catch(e){$('notice').textContent=e.message;}finally{$('settingsFields').disabled=false;}};
 $('refresh').onclick=()=>refresh().catch(e=>$('notice').textContent=e.message);refresh().catch(e=>$('notice').textContent=e.message);
})();
