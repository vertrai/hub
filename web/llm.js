let providers = [];
let presets = [];
let codexPreset = {models:[]};
let editingAPI = null;
let modelDrafts = new Map();
let selectedAPIPreset = "";
let editingSettings = null;
let oauthSession = null;
let reauthorizing = null;
let oauthBusy = false;
let oauthTimer = null;
const modelsFrom = value => value.split(/[\n,，]+/).map(s => s.trim()).filter(Boolean);
const providerPath = id => '/v1/admin/llm/providers/' + encodeURIComponent(id);
let llmAvailableModels = [];
let llmRouteRows = [];
let llmKeyRows = [];
let editingKeyPolicy = null;
let resourceSettingsLoaded = false;
const providerInput = p => ({name:p.name || p.id, kind:p.kind, preset:p.preset || '', baseUrl:p.baseUrl, models:p.models, enabled:p.enabled});
$("base").textContent = location.origin + "/llm/v1";

function providerLabel(p){if(!p)return '上游不可用';return p.name && p.name!==p.id ? p.name : (presets.find(x=>x.id===p.preset)?.name || (p.kind==='openai-codex'?'Codex 账号':'API Provider'));}
function modelTags(models){return models.map(m=>`<span class="model-tag">${esc(m)}</span>`).join('');}
function renderProvider(p) {
 const codex=p.kind==='openai-codex';
 return `<article class="provider"><div class="provider-heading"><div><span class="eyebrow">${codex?'CODEX ACCOUNT':'API PROVIDER'}</span><h3>${esc(providerLabel(p))}</h3></div><span class="pill ${p.enabled?'available':''}">${p.enabled?'已启用':'已停用'}</span></div><p class="provider-endpoint">${esc(codex?(p.email || p.accountId || '已连接账号'):p.baseUrl)}</p><div class="model-label">来源模型 <span>${p.models.length}</span></div><div class="model-tags">${modelTags(p.models)}</div><div class="provider-footer"><details><summary>连接详情</summary><code>${esc(p.id)}</code>${codex?`<p>凭据到期：${p.expiresAt?esc(new Date(p.expiresAt).toLocaleString()):'—'}</p>`:''}</details><div class="actions"><button type="button" class="secondary" data-edit="${esc(p.id)}">编辑</button>${codex?`<button type="button" class="secondary" data-reauthorize="${esc(p.id)}">重新授权</button>`:''}<button type="button" class="secondary" data-toggle="${esc(p.id)}">${p.enabled?'停用':'启用'}</button><button type="button" class="secondary danger-action" data-delete="${esc(p.id)}">删除</button></div></div></article>`;
}
async function refreshProviders() {
  const data = await adminRequest('/v1/admin/llm/providers');
  providers = data.items;
  const codex = providers.filter(p => p.kind === 'openai-codex');
  const api = providers.filter(p => p.kind !== 'openai-codex');
  $("codexCount").textContent = `(${codex.length})`;
  $("apiCount").textContent = `(${api.length})`;
  $("codexProviders").innerHTML = codex.map(renderProvider).join('') || '<p class="empty">尚未连接 Codex 账号。点击“添加 Codex 账号”开始 OAuth 授权。</p>';
  $("apiProviders").innerHTML = api.map(renderProvider).join('') || '<p class="empty">尚未配置 API Provider。选择服务商并填写 API Key 即可添加。</p>';
  await refreshLLMRouting();
}
async function refreshKeys() {
  const data = await adminRequest('/v1/admin/llm/keys');
  llmKeyRows=data.items;
  $("keys").innerHTML = data.items.map(k => `<div class="key-row"><div class="key-info"><div class="key-heading"><b>${esc(llmUsers.find(u=>u.id===k.ownerUserId || (u.accessKeys || []).some(a=>a.id===k.hubAccessKeyId))?.name || k.ownerUserId || k.name)} · LLM Key</b><span class="pill ${k.revoked?'':'available'}">${k.revoked?'已撤销':'可用'}</span><code>${esc(k.prefix)}…</code></div><div class="route-mapping"><span class="alias-label">hub-chat <small>默认模型</small></span><span class="route-arrow">→</span><b>${esc(k.defaultModel || '未设置')}</b></div><div class="model-tags">${modelTags(k.allowedModels || [])}</div><details class="key-details"><summary>归属信息</summary><code>${esc(k.hubAccessKeyId || '独立 Key')}</code></details></div><div class="actions">${k.revoked?'':`<button type="button" class="secondary" data-key-policy="${esc(k.id)}">修改策略</button><button type="button" class="secondary danger-action" data-revoke="${esc(k.id)}">撤销</button>`}</div></div>`).join('') || '<p class="empty">尚未创建密钥，选择用户与 Hub Key 后即可创建。</p>';

}
function presetHelp() {
  const preset = presets.find(p => p.id === $("apiPreset").value);
  $("presetHelp").textContent = preset?.help || '';
  $("presetDocs").hidden = !preset?.docsUrl;
  $("presetDocs").href = preset?.docsUrl || '#';
  $("appendPresetModels").hidden = !preset?.models?.length;
  $("modelPresetHelp").textContent = preset?.models?.length ? `官方对话模型预设，核对日期 ${preset.modelsCheckedAt}。可逐行增删，新模型直接追加后保存。` : '每行或逗号分隔，可随时追加模型后保存。';
}
function openAPI(p = null) {
  editingAPI = p;
  modelDrafts = new Map();
  $("apiForm").reset();
  $("apiTitle").textContent = p ? '编辑 ' + (p.name || p.id) : '添加 API Provider';
  $("apiName").value = p?.name || '';
  $("apiPreset").value = p?.preset || (p ? 'custom' : 'deepseek');
  $("apiBaseURL").value = p?.baseUrl || presets.find(p => p.id === 'deepseek')?.baseUrl || '';
  selectedAPIPreset = $("apiPreset").value;
  $("apiModels").value = p ? p.models.join('\n') : (presets.find(item => item.id === selectedAPIPreset)?.models || []).join('\n');
  $("apiEnabled").checked = p ? p.enabled : true;
  $("apiKey").required = !p;
  $("apiStatus").textContent = '';
  presetHelp();
  $("apiDialog").showModal();
}
$("appendPresetModels").onclick = () => {
  const defaults = presets.find(p => p.id === $("apiPreset").value)?.models || [];
  $("apiModels").value = [...new Set([...modelsFrom($("apiModels").value), ...defaults])].join('\n');
};
$("apiPreset").onchange = () => {
  modelDrafts.set(selectedAPIPreset, $("apiModels").value);
  selectedAPIPreset = $("apiPreset").value;
  $("apiModels").value = modelDrafts.has(selectedAPIPreset) ? modelDrafts.get(selectedAPIPreset) : (presets.find(p => p.id === selectedAPIPreset)?.models || []).join('\n');
  $("apiBaseURL").value = presets.find(p => p.id === $("apiPreset").value)?.baseUrl || '';
  // A key from a previous provider must not be sent to a newly selected host.
  $("apiKey").value = '';
  $("apiKey").required = true;
  presetHelp();
};
$("addAPI").onclick = () => openAPI();
$("closeAPI").onclick = () => $("apiDialog").close();
$("apiDialog").addEventListener('close', () => { $("apiKey").value = ''; });
$("apiForm").onsubmit = async e => {
  e.preventDefault();
  const button = e.submitter;
  setBusy(button, true);
  try {
    const input = {kind:'openai', name:$("apiName").value.trim(), preset:$("apiPreset").value, baseUrl:$("apiBaseURL").value.trim(), models:modelsFrom($("apiModels").value), enabled:$("apiEnabled").checked};
    if ($("apiKey").value.trim()) input.apiKey = $("apiKey").value.trim();
    await adminRequest(editingAPI ? providerPath(editingAPI.id) : '/v1/admin/llm/providers', {method:editingAPI ? 'PUT' : 'POST', body:JSON.stringify(input)});
    $("apiDialog").close();
    showStatus($("providerStatus"), 'Provider 已保存', true);
    await refreshProviders();
  } catch (err) { showStatus($("apiStatus"), err.message); }
  finally { setBusy(button, false); }
};

function openOAuth(p = null) {
  if (oauthSession) { $("oauthDialog").showModal(); return; }
  reauthorizing = p;
  $("oauthAccountHelp").textContent = p ? "输入 Code 并完成授权。重新授权必须登录此 Provider 原来绑定的 OpenAI 账号。" : "输入 Code 并完成授权。如需连接其他账号，请在 OpenAI 页面切换账号。";
  $("oauthForm").reset();
  $("oauthForm").hidden = false;
  $("oauthTitle").textContent = p ? '重新授权 ' + (p.name || p.id) : '添加 Codex 账号';
  $("oauthName").value = p?.name || '';
  $("oauthModels").value = p ? p.models.join('\n') : codexPreset.models.join('\n');
  $("oauthResult").hidden = true;
  $("oauthStatus").textContent = '';
  setOAuthFields(false);
  updateOAuthModelPreview();
  $("oauthDialog").showModal();
}
function updateOAuthModelPreview(){
 $("oauthModelPreview").innerHTML=modelTags([...new Set(modelsFrom($("oauthModels").value))]);
}
$("appendCodexModels").onclick=()=>{if(reauthorizing || oauthSession)return;$("oauthModels").value=[...new Set([...modelsFrom($("oauthModels").value),...codexPreset.models])].join('\n');updateOAuthModelPreview();};
$("oauthModels").oninput=updateOAuthModelPreview;
$("copyOAuthCode").onclick=async()=>{
 if(!oauthSession)return;
 try{await navigator.clipboard.writeText($("oauthCode").textContent);showStatus($("oauthStatus"),'授权码已复制，请在 OpenAI 页面粘贴。',true);}catch{showStatus($("oauthStatus"),'无法自动复制，请选中授权码手动复制。');}
};
function setOAuthFields(pending) {
 $("oauthStepSetup").classList.toggle('current',!pending);
 $("oauthStepAuthorize").classList.toggle('current',pending);
  for (const id of ['oauthName', 'oauthModels']) $(id).readOnly = pending || !!reauthorizing;
  $("startOAuth").disabled = pending || oauthBusy;
}
function updateOAuthClock() {
  if (!oauthSession) return;
  const remaining = Math.max(0, Math.ceil((Date.parse(oauthSession.expiresAt) - Date.now()) / 1000));
  const wait = Math.max(0, Math.ceil((oauthSession.nextPoll - Date.now()) / 1000));
  $("oauthExpiry").textContent = remaining ? `授权码有效期剩余 ${Math.floor(remaining / 60)} 分 ${remaining % 60} 秒` : '授权码已过期，请取消后重新生成。';
  $("completeOAuth").disabled = oauthBusy || !remaining || wait > 0;
  $("completeOAuth").textContent = oauthBusy ? '连接中…' : wait ? `完成连接（${wait} 秒后）` : '完成连接';
}
function clearOAuth() {
  clearInterval(oauthTimer);
  oauthTimer = null;
  oauthSession = null;
  $("oauthResult").hidden = true;
  $("oauthForm").hidden = false;
  $("oauthCode").textContent = '';
  setOAuthFields(false);
}
$("addCodex").onclick = () => openOAuth();
$("closeOAuth").onclick = () => $("oauthDialog").close();
$("oauthForm").onsubmit = async e => {
  e.preventDefault();
  if (oauthBusy || oauthSession) return;
  oauthBusy = true;
  setBusy($("startOAuth"), true);
  setOAuthFields(true);
  try {
    const input = {providerId:reauthorizing?.id, name:$("oauthName").value.trim(), models:modelsFrom($("oauthModels").value), reauthorize:!!reauthorizing};
    const data = await adminRequest('/v1/admin/llm/oauth/device/start', {method:'POST', body:JSON.stringify(input)});
    oauthSession = {...data, nextPoll:Date.now() + data.interval * 1000};
    $("oauthCode").textContent = data.userCode;
    $("oauthForm").hidden = true;
    $("oauthTarget").textContent = `${input.name || "Codex 账号"} · 等待授权`;
    $("oauthResult").hidden = false;
    showStatus($("oauthStatus"), '请在 OpenAI 页面完成授权，再回到这里完成连接。', true);
    clearInterval(oauthTimer);
    oauthTimer = setInterval(updateOAuthClock, 1000);
  } catch (err) { showStatus($("oauthStatus"), err.message); }
  finally { oauthBusy = false; setBusy($("startOAuth"), false); setOAuthFields(!!oauthSession); updateOAuthClock(); }
};
$("completeOAuth").onclick = async () => {
  if (!oauthSession || oauthBusy) return;
  oauthBusy = true;
  updateOAuthClock();
  $("cancelOAuth").disabled = true;
  try {
    const data = await adminRequest('/v1/admin/llm/oauth/device/complete', {method:'POST', body:JSON.stringify({state:oauthSession.state})});
    if (data.status === 'authorization_pending') {
      oauthSession.nextPoll = Date.now() + (data.retryAfter || 5) * 1000;
      showStatus($("oauthStatus"), 'OpenAI 授权尚未完成，请授权后再次点击“完成连接”。', true);
      return;
    }
    clearOAuth();
    $("oauthDialog").close();
    showStatus($("providerStatus"), `${data.name || data.providerId} 已连接，可继续添加其他 Codex 账号。`, true);
    await refreshProviders();
  } catch (err) { showStatus($("oauthStatus"), err.message); }
  finally { oauthBusy = false; $("cancelOAuth").disabled = false; setOAuthFields(!!oauthSession); updateOAuthClock(); }
};
$("cancelOAuth").onclick = async () => {
  if (!oauthSession || oauthBusy) return;
  try {
    await adminRequest('/v1/admin/llm/oauth/device/' + encodeURIComponent(oauthSession.state), {method:'DELETE'});
    clearOAuth();
    showStatus($("oauthStatus"), '本次授权已取消，可以重新生成。', true);
  } catch (err) {
    if (err.status === 404) { clearOAuth(); showStatus($("oauthStatus"), '会话已失效，可以重新生成。', true); }
    else showStatus($("oauthStatus"), err.message);
  }
};

function openSettings(p) {
  editingSettings = p;
  $("settingsName").value = p.name || p.id;
  $("settingsModels").value = p.models.join('\n');
  $("settingsEnabled").checked = p.enabled;
  $("settingsStatus").textContent = '';
  $("settingsDialog").showModal();
}
$("closeSettings").onclick = () => $("settingsDialog").close();
$("settingsForm").onsubmit = async e => {
  e.preventDefault();
  setBusy(e.submitter, true);
  try {
    const input = {...providerInput(editingSettings), name:$("settingsName").value.trim(), models:modelsFrom($("settingsModels").value), enabled:$("settingsEnabled").checked};
    await adminRequest(providerPath(editingSettings.id), {method:'PUT', body:JSON.stringify(input)});
    $("settingsDialog").close();
    await refreshProviders();
  } catch (err) { showStatus($("settingsStatus"), err.message); }
  finally { setBusy(e.submitter, false); }
};
async function providerAction(e) {
  const button = e.target.closest('button');
  if (!button) return;
  const id = button.dataset.edit || button.dataset.reauthorize || button.dataset.toggle || button.dataset.delete;
  const p = providers.find(p => p.id === id);
  if (!p) return;
  if (button.dataset.edit) { p.kind === 'openai-codex' ? openSettings(p) : openAPI(p); return; }
  if (button.dataset.reauthorize) { openOAuth(p); return; }
  try {
    if (button.dataset.delete) {
      if (!confirm('删除此 Provider？使用它的请求将不可用。')) return;
      await adminRequest(providerPath(p.id), {method:'DELETE'});
    } else {
      await adminRequest(providerPath(p.id), {method:'PUT', body:JSON.stringify({...providerInput(p), enabled:!p.enabled})});
    }
    await refreshProviders();
  } catch (err) { showStatus($("providerStatus"), err.message); }
}
$("codexProviders").onclick = providerAction;
$("apiProviders").onclick = providerAction;
let llmUsers=[];let selectedBoundResource=null;let boundSelectionVersion=0;let creatingBoundOwner=null;
function boundPath(userId, keyId){return '/v1/admin/access-keys/'+encodeURIComponent(keyId)+'/llm-resource?userId='+encodeURIComponent(userId);}
async function loadBoundSelection(){
 const version=++boundSelectionVersion;selectedBoundResource=null;$("issued").hidden=true;$("createBoundKey").disabled=true;$("editBoundKey").hidden=true;
 const userId=$("llmUser").value,keyId=$("llmHubKey").value;
 if(!userId || !keyId){showStatus($("keyStatus"),'请选择有 Hub Key 的用户。');return;}
 try{const data=await adminRequest(boundPath(userId,keyId));if(version!==boundSelectionVersion)return;
 selectedBoundResource=data;
 if(data.exists){$("issued").hidden=false;$("issued").textContent=data.revoked?'LLM Key 已撤销':'hub-chat（默认模型）→ '+data.defaultModel+'\n\n'+JSON.stringify({apiKey:data.apiKey,baseUrl:data.baseUrl,model:data.model},null,2)+'\n\nHermes 配置（连接 Hub）\n'+JSON.stringify({llm_provider:data.provider,llm_base_url:data.baseUrl,llm_api_key:data.apiKey,llm_model:data.model},null,2);$("editBoundKey").hidden=data.revoked;showStatus($("keyStatus"),'此 Hub Key 已创建 LLM Key。',true);}
 else{$("createBoundKey").disabled=false;showStatus($("keyStatus"),'此 Hub Key 尚未创建 LLM Key。',true);}
 }catch(err){if(version===boundSelectionVersion)showStatus($("keyStatus"),err.message);}
}
$("llmUser").onchange=()=>{const user=llmUsers.find(u=>u.id===$("llmUser").value);$("llmHubKey").innerHTML=(user?.accessKeys || []).filter(k=>k.status==='active').map(k=>`<option value="${esc(k.id)}">${esc(k.name && k.name!==k.id ? k.name+" · "+k.id : k.id)}</option>`).join('');loadBoundSelection();};
$("llmHubKey").onchange=loadBoundSelection;
$("createBoundKey").onclick=()=>{
 creatingBoundOwner={userId:$("llmUser").value,keyId:$("llmHubKey").value};
 $("createKeyOwner").textContent=$('llmUser').selectedOptions[0].textContent+' / '+creatingBoundOwner.keyId;
 fillModelChoices('keyModels',[]);fillDefaultChoice('keyDefault',[],'');
 renderKeyPolicyChoices('key',[], '');
 $("createKeyStatus").textContent='';$("createKeyDialog").showModal();
};
function updateKeyPolicyRoute(prefix){
 const allowed=selectedModels(prefix+'Models'),model=$(prefix+'Default').value;
 $(prefix+'SelectionCount').textContent=`已授权 ${allowed.length} 个对外模型`;
 $(prefix+'RoutePreview').textContent=model?'hub-chat → '+model:'未设置默认路由';
}
function renderKeyPolicyChoices(prefix,allowed,current){
 fillModelChoices(prefix+'Models',allowed);
 $(prefix+'ModelChecks').innerHTML=llmAvailableModels.map(m=>`<label class="model-choice"><input type="checkbox" value="${esc(m.id)}" ${allowed.includes(m.id)?'checked':''}><span>${esc(m.id)}</span></label>`).join('') || '<p>请先配置有效的对外模型路由。</p>';
 fillDefaultChoice(prefix+'Default',selectedModels(prefix+'Models'),current);updateKeyPolicyRoute(prefix);
}
for(const prefix of ['key','policy']){
 $(prefix+'ModelChecks').onchange=()=>{
 const allowed=Array.from($(prefix+'ModelChecks').querySelectorAll('input:checked')).map(x=>x.value);
 const previous=$(prefix+'Default').value;
 fillModelChoices(prefix+'Models',allowed);fillDefaultChoice(prefix+'Default',allowed,previous);
 // Removing the current default requires an explicit replacement.
 if(previous && !allowed.includes(previous))$(prefix+'Default').value='';
 updateKeyPolicyRoute(prefix);
 };
 $(prefix+'Default').onchange=()=>updateKeyPolicyRoute(prefix);
}

$("cancelCreateKey").onclick=()=>$("createKeyDialog").close();
$("editBoundKey").onclick=()=>openKeyPolicy({id:selectedBoundResource.keyId,name:'Hub '+$("llmHubKey").value,allowedModels:selectedBoundResource.allowedModels,defaultModel:selectedBoundResource.defaultModel});
$("keyForm").onsubmit=async e=>{
 e.preventDefault();setBusy(e.submitter,true);
 try{await adminRequest(boundPath(creatingBoundOwner.userId,creatingBoundOwner.keyId),{method:'POST',body:JSON.stringify({...creatingBoundOwner,allowedModels:selectedModels('keyModels'),defaultModel:$("keyDefault").value})});$("createKeyDialog").close();await refreshKeys();await loadBoundSelection();}
 catch(err){showStatus($("createKeyStatus"),err.message);}finally{setBusy(e.submitter,false);}
};
(async()=>{try{const data=await adminRequest('/v1/admin/users');llmUsers=data.items;$("llmUser").innerHTML='<option value="">请选择用户</option>'+llmUsers.map(u=>`<option value="${esc(u.id)}">${esc(u.name || u.id)}</option>`).join('');await refreshKeys();}catch(err){showStatus($("keyStatus"),err.message);}})();
$("keys").onclick = async e => {
  const policyButton=e.target.closest('[data-key-policy]');
  if(policyButton){openKeyPolicy(llmKeyRows.find(k=>k.id===policyButton.dataset.keyPolicy));return;}
  const button = e.target.closest('[data-revoke]');
  if (!button || !confirm('撤销此密钥？使用它的客户端将无法访问中转站。')) return;
  try { await adminRequest('/v1/admin/llm/keys/' + encodeURIComponent(button.dataset.revoke), {method:'DELETE'}); await refreshKeys(); }
  catch (err) { showStatus($("keyStatus"), err.message); }
};
(async () => {
  try {
    const data = await adminRequest('/v1/admin/llm/presets');
    presets = data.items;
    codexPreset=data.codex || {models:[]};
    $("codexPresetDate").textContent=codexPreset.modelsCheckedAt || "";
    $("apiPreset").innerHTML = presets.map(p => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join('');
    await refreshProviders();
  } catch (err) { showStatus($("providerStatus"), err.message); }
})();
refreshKeys().catch(e => showStatus($("keyStatus"), e.message));

function selectedModels(id) {return Array.from($(id).selectedOptions).map(option=>option.value);}
function fillModelChoices(id,selected) {
  const models=[...llmAvailableModels];
  // Only published, available routes can be selected for a key.
  $(id).innerHTML=models.map(m=>`<option value="${esc(m.id)}" ${selected.includes(m.id)?'selected':''}>${esc(m.name && m.name!==m.id ? m.name+' · '+m.id : m.id)}</option>`).join('');
}
function fillDefaultChoice(id,allowed,current) {
  allowed=allowed.filter(id=>llmAvailableModels.some(m=>m.id===id));
  $(id).innerHTML='<option value="">请选择默认模型</option>'+allowed.map(model=>`<option value="${esc(model)}">hub-chat（默认模型）→ ${esc(model)}</option>`).join('');
  $(id).value=allowed.includes(current)?current:(allowed[0] || '');
}
async function refreshLLMRouting() {
  const [routes,settings]=await Promise.all([adminRequest('/v1/admin/llm/routes'),adminRequest('/v1/admin/llm/resource-settings')]);
  llmAvailableModels=routes.availableModels;llmRouteRows=routes.items || [];
  $("llmRoutes").innerHTML=llmRouteRows.map(r=>`<div class="route-row"><div class="route-public"><span class="eyebrow">对外模型</span><b>${esc(r.id)}</b>${r.name!==r.id?`<small>${esc(r.name)}</small>`:''}</div><span class="route-arrow">→</span><div class="route-upstream"><b>${esc(r.upstreamModel)}</b><small>${esc(providerLabel(providers.find(p=>p.id===r.providerId)))}</small></div><div class="actions"><button type="button" class="secondary" data-route="${esc(r.id)}">编辑</button><button type="button" class="secondary danger-action" data-delete-route="${esc(r.id)}">删除</button></div></div>`).join('') || '<p class="empty">尚未发布对外模型，在下方创建第一条路由。</p>';
  $("routeTarget").innerHTML=providers.filter(p=>p.enabled).map(p=>`<optgroup label="${esc(providerLabel(p))} · ${esc(p.id.slice(-6))}">${p.models.map(m=>`<option value="${esc(p.id+'/'+m)}">${esc(m)}</option>`).join('')}</optgroup>`).join('');
  const currentKeyModels=selectedModels('keyModels');const currentKeyDefault=$("keyDefault").value;
  fillModelChoices('keyModels',currentKeyModels.length?currentKeyModels:settings.allowedModels);
  fillDefaultChoice('keyDefault',selectedModels('keyModels'),currentKeyDefault || settings.defaultModel);
  if(!resourceSettingsLoaded){
    $("resourceBaseURL").value=settings.baseUrl || location.origin+'/llm/v1';
    fillModelChoices('resourceModels',settings.allowedModels);fillDefaultChoice('resourceDefault',settings.allowedModels,settings.defaultModel);resourceSettingsLoaded=true;
  }else{const allowed=selectedModels('resourceModels');const current=$("resourceDefault").value;fillModelChoices('resourceModels',allowed);fillDefaultChoice('resourceDefault',allowed,current);}
  renderResourceModelChecks();
}
function updateResourcePreview(){
 const allowed=selectedModels('resourceModels'),model=$("resourceDefault").value;
 $("resourceSelectedCount").textContent=`已选 ${allowed.length} 个`;
 $("resourceSummary").textContent=allowed.length?`${allowed.length} 个模型`:'未选择模型';
 $("resourceRoutePreview").textContent=model?'hub-chat（默认模型） → '+model:'请先选择允许的模型';
}
function renderResourceModelChecks(){
 const allowed=selectedModels('resourceModels');
 $("resourceModelChecks").innerHTML=llmAvailableModels.map(m=>`<label class="model-choice"><input type="checkbox" value="${esc(m.id)}" ${allowed.includes(m.id)?'checked':''}><span>${esc(m.id)}</span></label>`).join('') || '<p class="settings-hint">暂无有效对外模型，请先创建模型路由。</p>';
 updateResourcePreview();
}
$("resourceModelChecks").onchange=()=>{
 const allowed=Array.from($("resourceModelChecks").querySelectorAll('input:checked')).map(input=>input.value);
 fillModelChoices('resourceModels',allowed);fillDefaultChoice('resourceDefault',allowed,$("resourceDefault").value);updateResourcePreview();
};
$("resourceDefault").onchange=updateResourcePreview;
$("resourceModels").onchange=()=>{fillDefaultChoice('resourceDefault',selectedModels('resourceModels'),$("resourceDefault").value);renderResourceModelChecks();};
$("keyModels").onchange=()=>fillDefaultChoice('keyDefault',selectedModels('keyModels'),$("keyDefault").value);
$("policyModels").onchange=()=>fillDefaultChoice('policyDefault',selectedModels('policyModels'),$("policyDefault").value);
$("stationForm").onsubmit=async e=>{e.preventDefault();setBusy(e.submitter,true);try{
 await adminRequest('/v1/admin/llm/resource-settings',{method:'PUT',body:JSON.stringify({baseUrl:$("resourceBaseURL").value.trim()})});
 showStatus($("stationStatus"),'访问地址已保存。',true);
}catch(err){showStatus($("stationStatus"),err.message);}finally{setBusy(e.submitter,false);}};
$("resourceForm").onsubmit=async e=>{e.preventDefault();setBusy(e.submitter,true);try{
  await adminRequest('/v1/admin/llm/resource-settings',{method:'PUT',body:JSON.stringify({allowedModels:selectedModels('resourceModels'),defaultModel:$("resourceDefault").value})});
  showStatus($("resourceStatus"),'已保存，新申请的 Key 使用此策略。',true);
}catch(err){showStatus($("resourceStatus"),err.message);}finally{setBusy(e.submitter,false);}};
$("routeForm").onsubmit=async e=>{e.preventDefault();setBusy(e.submitter,true);try{
  const target=$("routeTarget").value;const slash=target.indexOf('/');
  await adminRequest('/v1/admin/llm/routes',{method:'PUT',body:JSON.stringify({id:$("routeID").value.trim(),name:$("routeName").value.trim(),providerId:target.slice(0,slash),upstreamModel:target.slice(slash+1)})});
  await refreshLLMRouting();showStatus($("routeStatus"),'路由已更新，后续请求生效。',true);
}catch(err){showStatus($("routeStatus"),err.message);}finally{setBusy(e.submitter,false);}};
$("llmRoutes").onclick=async e=>{
const remove=e.target.closest('[data-delete-route]');
if(remove){
 if(!confirm('删除对外模型 '+remove.dataset.deleteRoute+'？使用该模型的请求将不可用；相关 Key 的默认模型需要另行调整。'))return;
 try{await adminRequest('/v1/admin/llm/routes/'+encodeURIComponent(remove.dataset.deleteRoute),{method:'DELETE'});await refreshLLMRouting();showStatus($("routeStatus"),'路由已删除，请检查相关 Key 和自动申请策略的默认模型。',true);}catch(err){showStatus($("routeStatus"),err.message);}return;
}
const button=e.target.closest('[data-route]');if(!button)return;const route=llmRouteRows.find(r=>r.id===button.dataset.route);$("routeEditor").open=true;$("routeID").value=route.id;$("routeName").value=route.name;$("routeTarget").value=route.providerId+'/'+route.upstreamModel;};
function openKeyPolicy(key){
  editingKeyPolicy=key;$("keyPolicyTitle").textContent=key.name+' · 模型策略';renderKeyPolicyChoices('policy',key.allowedModels || [],key.defaultModel);$("keyPolicyStatus").textContent='';$("keyPolicyDialog").showModal();
}
$("closeKeyPolicy").onclick=()=>$("keyPolicyDialog").close();
$("keyPolicyForm").onsubmit=async e=>{e.preventDefault();setBusy(e.submitter,true);try{
  await adminRequest('/v1/admin/llm/keys/'+encodeURIComponent(editingKeyPolicy.id)+'/policy',{method:'PATCH',body:JSON.stringify({allowedModels:selectedModels('policyModels'),defaultModel:$("policyDefault").value})});
  $("keyPolicyDialog").close();await refreshKeys();await loadBoundSelection();showStatus($("keyStatus"),'策略已更新；使用 hub-chat 的 Agent 下次请求即使用新的默认模型。',true);
}catch(err){showStatus($("keyPolicyStatus"),err.message);}finally{setBusy(e.submitter,false);}};
