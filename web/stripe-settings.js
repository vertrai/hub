(() => {
 function cell(row,value){const td=row.insertCell();td.textContent=value||'—';return td;}
 async function refresh(){
 const [cfg,catalog,bills]=await Promise.all([adminRequest('/v1/admin/stripe/settings'),adminRequest('/v1/admin/agent-catalog'),adminRequest('/v1/admin/billing')]);
 $('serviceState').textContent=`支付${cfg.enabled?'已启用':'未启用'} · Secret Key ${cfg.secretKeyConfigured?'已配置':'未配置'} · Webhook Secret ${cfg.webhookSecretConfigured?'已配置':'未配置'}`;
 $('products').replaceChildren();
 for(const agent of catalog.agents){
 const row=$('products').insertRow();cell(row,agent.name);cell(row,agent.published?'已上架':'未上架');
 const product=document.createElement('input');product.value=agent.productId||'';product.readOnly=!!agent.productId;product.placeholder='例如 x_agent';product.setAttribute('aria-label',agent.name+' 商品标识');cell(row,'').replaceChildren(product);
 const price=document.createElement('input');price.value=agent.stripePriceId||'';price.placeholder='price_…';price.setAttribute('aria-label',agent.name+' Stripe Price ID');cell(row,'').replaceChildren(price);
 const button=document.createElement('button');button.textContent='保存价格';cell(row,'').replaceChildren(button);
 button.onclick=async()=>{button.disabled=true;$('notice').textContent='';try{const data=await adminRequest('/v1/admin/stripe/products/'+encodeURIComponent(agent.id),{method:'PATCH',body:JSON.stringify({productId:product.value.trim(),stripePriceId:price.value.trim()})});product.value=data.productId;product.readOnly=!!data.productId;price.value=data.stripePriceId;$('notice').textContent=agent.name+' 的订阅价格已保存';}catch(e){$('notice').textContent=e.message;}finally{button.disabled=false;}};
 }
 if(!catalog.agents.length){const td=$('products').insertRow().insertCell();td.colSpan=5;td.textContent='暂无助手，请先在助手管理中创建。';}
 $('billing').replaceChildren();for(const b of bills.items){const row=$('billing').insertRow();for(const value of [b.id,b.product,b.status,b.subscriptionId,b.agentId,b.currentPeriodEnd&&!b.currentPeriodEnd.startsWith('0001')?new Date(b.currentPeriodEnd).toLocaleString():'—'])cell(row,value);}
 if(!bills.items.length){const td=$('billing').insertRow().insertCell();td.colSpan=6;td.textContent='暂无订阅账单';}
 }
 $('refresh').onclick=()=>refresh().catch(e=>$('notice').textContent=e.message);refresh().catch(e=>$('notice').textContent=e.message);
})();
