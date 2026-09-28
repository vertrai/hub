(() => {
  const scope = location.pathname.endsWith('/web') ? 'web' : 'core';
  const endpoint = '/v1/admin/catalog-management/' + scope;
  const form = $('editor');
  let selected = null, generation = 0, uploading = false, resourceOptions = null;
  const coreFields = [['name','助手名称'],['logoUrl','默认图标地址（HTTPS 或已上传的图片路径）'],['intro','一句话介绍'],['module','Hymatrix 运行模块']];
  const copyFields = [['name','展示名称'],['intro','一句话介绍'],['summary','详细介绍'],['capabilities','能力（每行一项）'],['compatibilityNote','使用条件']];
  $('heading').textContent = scope === 'core' ? '助手库' : '网页版展示';
  $('description').textContent = scope === 'core' ? '维护共用的助手身份、基础信息与运行模块。新增助手后，分别前往各入口配置展示与上架。' : '选择助手库中的助手，独立设置网站文案、排序、上架与购买方式，不会修改微信小程序。';
  document.title = $('heading').textContent + ' · Hub';
  $('new').textContent = scope === 'core' ? '新增助手' : '前往助手库';
  function field(parent, name, label, type='text') {
    const wrapper = document.createElement('label'); wrapper.textContent = label;
    const input = document.createElement(type === 'textarea' ? 'textarea' : 'input');
    input.name = name;
    if(type !== 'textarea') input.type=type;
    if(type==='checkbox')wrapper.className='check';
    if(type==='number')input.step='1';
    wrapper.append(input);parent.append(wrapper);return input;
  }
  if(scope==='core') {
    coreFields.forEach(([key,label])=>{
      const input=field($('fields'),key,label,['intro','summary','capabilities'].includes(key)?'textarea':'text');
      input.required=key!=='summary';
      input.maxLength=key==='name'?24:key==='module'?512:2000;
    });
    const resources=document.createElement('fieldset');resources.id='required-resources';
    resources.innerHTML='<legend>所需 Hub 资源</legend><p class="note">不勾选则不检查资源。小程序创建前会检查全部所选资源。</p>';
    $('fields').append(resources);
    const upload=field($('fields'),'iconFile','上传默认图标（PNG/JPEG，最大 5 MB）','file');upload.accept='image/png,image/jpeg';
    upload.onchange=async()=>{
      const file=upload.files[0];if(!file)return;
      if(!['image/png','image/jpeg'].includes(file.type)||file.size>5*1024*1024){showStatus($('status'),'请选择不超过 5 MB 的 PNG 或 JPEG 图片。');return;}
      const current=generation;uploading=true;setBusy($('save'),true);upload.disabled=true;
      try{const result=await adminRequest('/v1/admin/agent-catalog-images',{method:'POST',body:file,headers:{'Content-Type':file.type}});if(current===generation){form.elements.logoUrl.value=result.url;showStatus($('status'),'图片已上传，保存后生效。',true);}}
      catch(error){if(current===generation)showStatus($('status'),error.message);}finally{uploading=false;setBusy($('save'),false);upload.disabled=false;}
    };
  } else {
    $('fields').innerHTML = `<div class="web-toolbar"><div class="web-tabs" aria-label="预览页面"><button type="button" data-view="card" aria-pressed="true">广场卡片</button><button type="button" data-view="detail" aria-pressed="false">助手详情</button><button type="button" data-view="settings" aria-pressed="false">使用与订阅设置</button></div><div class="web-tabs" aria-label="展示语言"><button type="button" data-locale="zh" aria-pressed="true">中文</button><button type="button" data-locale="en" aria-pressed="false">English</button></div></div><p class="note">直接点击预览中的文字编辑；留空沿用默认文案。修改后点击保存，上架按钮会同时保存当前修改。</p><div id="web-stage"></div><div id="web-settings" hidden></div>`;
    const state=field($('web-settings'),'published','上架状态','checkbox');state.parentElement.hidden=true;
    field($('web-settings'),'inviteEnabled','允许邀请码创建','checkbox');
    field($('web-settings'),'subscriptionEnabled','允许 Stripe 订阅创建','checkbox');
    const modeNote=document.createElement('p');modeNote.className='note';modeNote.textContent='两项都选支持两种创建方式；两项都不选则允许免费创建。';$('web-settings').append(modeNote);
    field($('web-settings'),'sortOrder','网页排序（数字越小越靠前）','number');
    field($('web-settings'),'stripePriceId','Stripe 价格 ID（price_ 开头，留空时不提供订阅）');
    for(const locale of ['zh','en']) {
      const panel=document.createElement('div');panel.dataset.copyLocale=locale;panel.hidden=locale!=='zh';
      panel.innerHTML=`<div class="web-render-card"><div class="web-render-top"><img data-web-logo alt=""><span class="web-preview-label">${locale==='zh'?'AI 助手':'AI assistant'}</span></div><div data-copy-slot="name"></div><div data-copy-slot="intro"></div><div data-copy-slot="capabilities"></div><div class="web-card-link">${locale==='zh'?'查看助手':'View assistant'} <span>↗</span></div><div class="web-detail-copy" hidden><h3>${locale==='zh'?'关于这位助手':'About this assistant'}</h3><div data-copy-slot="summary"></div><h3>${locale==='zh'?'使用条件':'Before you start'}</h3><div data-copy-slot="compatibilityNote"></div></div></div>`;
      for(const [key,label] of copyFields){const input=field(panel.querySelector(`[data-copy-slot="${key}"]`),locale+'_'+key,label,'textarea');input.maxLength=2000;input.className='web-inline web-inline-'+key;input.setAttribute('aria-label',label+' · '+locale);input.parentElement.className='web-editable';}
      const aside=document.createElement('aside');aside.className='web-acquire-preview';aside.hidden=true;
      const zh=locale==='zh';
      aside.innerHTML=`<h2>${zh?'开始使用':'Get started'}</h2><div class="web-consent-preview"><div>${zh?'使用前请阅读':'Before you continue'}</div><div class="web-legal-links"><a href="https://vertr.ai/legal-terms.html" target="_blank" rel="noopener noreferrer">${zh?'使用条款':'Terms'}</a><a href="https://vertr.ai/legal-privacy.html" target="_blank" rel="noopener noreferrer">${zh?'隐私政策':'Privacy'}</a><a href="https://vertr.ai/legal-refund.html" target="_blank" rel="noopener noreferrer">${zh?'退款说明':'Refunds'}</a></div></div><p class="preview-agree">${zh?'用户打开统一使用说明并确认同意后，才能创建或订阅。':'Users must open and accept the shared notice before creating or subscribing.'}</p><div data-preview-subscribe><button type="button" disabled>${zh?'订阅助手':'Subscribe'}</button><p class="note">${zh?'价格和计费周期将在结账时确认。':'Review pricing and billing at checkout.'}</p></div><div data-preview-invite><label>${zh?'邀请码':'Invite code'}<input disabled aria-label="${zh?'邀请码预览':'Invite code preview'}"></label><button type="button" disabled>${zh?'创建助手':'Create assistant'}</button></div><p data-preview-unavailable>${zh?'免费创建助手（阅读并同意后可用）':'Create for free after accepting the notice'}</p><p class="note">${zh?'已登录状态预览 · 不会触发真实操作':'Signed-in preview · actions disabled'}</p>`;

      panel.append(aside);
      const capSection=document.createElement('section');capSection.className='web-cap-section';
      const capTitle=document.createElement('h3');capTitle.textContent=zh?'我能帮你':'How I can help';capSection.append(capTitle);
      const capSlot=panel.querySelector('[data-copy-slot="capabilities"]');capSlot.before(capSection);capSection.append(capSlot);
      $('web-stage').append(panel);
    }
    const publish=document.createElement('button');publish.type='submit';publish.id='publish';publish.name='publication';publish.className='secondary';$('save').after(publish);
    let locale='zh',view='card';
    function switchPreview(){
      document.querySelectorAll('[data-copy-locale]').forEach(el=>el.hidden=el.dataset.copyLocale!==locale);
      document.querySelectorAll('[data-locale]').forEach(el=>el.setAttribute('aria-pressed',String(el.dataset.locale===locale)));
      document.querySelectorAll('[data-view]').forEach(el=>el.setAttribute('aria-pressed',String(el.dataset.view===view)));
      $('web-stage').hidden=view==='settings';$('web-settings').hidden=view!=='settings';
      $('web-stage').classList.toggle('detail-view',view==='detail');
      document.querySelectorAll('.web-detail-copy,.web-acquire-preview').forEach(el=>el.hidden=view!=='detail');
      document.querySelectorAll('[data-copy-locale]').forEach(panel=>{const caps=panel.querySelector('.web-cap-section');if(view==='detail')panel.querySelector('.web-detail-copy [data-copy-slot=summary]').after(caps);else panel.querySelector('.web-card-link').before(caps);});
    }
    document.querySelectorAll('[data-locale]').forEach(el=>el.onclick=()=>{locale=el.dataset.locale;switchPreview();});
    document.querySelectorAll('[data-view]').forEach(el=>el.onclick=()=>{view=el.dataset.view;switchPreview();});
  }

  function edit(agent) {
    generation++;selected=agent;$("delete").hidden=scope!=="core"||!agent;form.reset();form.hidden=false;$('selection-hint').hidden=true;$('status').textContent='';
    $('editor-title').textContent=agent?agent.name:'新增助手';$('identity').textContent=agent?'助手 ID：'+agent.id:'保存后生成唯一助手 ID，默认不在任何入口上架。';
    if(scope==='core')coreFields.forEach(([key])=>{form.elements[key].value=Array.isArray(agent?.[key])?agent[key].join('\n'):agent?.[key]||'';});
    else {
      form.elements.published.checked=!!agent.web?.published;form.elements.inviteEnabled.checked=agent.web?.inviteEnabled??true;form.elements.subscriptionEnabled.checked=agent.web?.subscriptionEnabled??!!agent.stripePriceId;
      form.elements.sortOrder.value=agent.web?.sortOrder||0;
      form.elements.stripePriceId.value=agent.stripePriceId||'';
      for(const locale of ['zh','en']) for(const [key] of copyFields){const v=agent.web?.content?.[locale]?.[key];form.elements[locale+'_'+key].value=Array.isArray(v)?v.join('\n'):v||'';}
    }
    document.querySelectorAll('[data-agent-id]').forEach(button=>button.setAttribute('aria-pressed',String(button.dataset.agentId===agent?.id)));
    if(scope==='core')document.querySelectorAll('[data-resource]').forEach(input=>{input.checked=(agent?.requiredResources||[]).includes(input.dataset.resource);});
    preview();
  }
  function preview(){
    if(scope!=='web'||!selected)return;
    $('preview').hidden=true;
    document.querySelectorAll('[data-preview-invite]').forEach(el=>el.hidden=!form.elements.inviteEnabled.checked);
    document.querySelectorAll('[data-preview-subscribe]').forEach(el=>el.hidden=!form.elements.subscriptionEnabled.checked);
    document.querySelectorAll('[data-preview-unavailable]').forEach(el=>el.hidden=form.elements.inviteEnabled.checked||form.elements.subscriptionEnabled.checked);
    $('publish').textContent=selected.web?.published?'保存并下架':'保存并上架';
    for(const locale of ['zh','en'])for(const [key] of copyFields){
      const input=form.elements[locale+'_'+key];
      const fallback=form.elements[(locale==='zh'?'en':'zh')+'_'+key].value.trim()||(['summary','capabilities','compatibilityNote','consentText'].includes(key)?'':selected[key])||'';
      input.placeholder=(Array.isArray(fallback)?fallback.join(' · '):fallback)||({summary:'点击编辑详细介绍',compatibilityNote:'点击编辑使用条件',consentText:'点击编辑使用说明'}[key]||'点击编辑');
    }
    document.querySelectorAll('[data-web-logo]').forEach(img=>{const url=selected.logoUrl||'';img.hidden=!url;if(url.startsWith('https://')||url.startsWith('/'))img.src=url;});
  }
  form.addEventListener('input',preview);
  async function refresh(){
    try {
      const {agents,resourceOptions:options}=await adminRequest(endpoint);
      if(scope==='core' && resourceOptions===null){
        if(!Array.isArray(options))throw new Error('无法加载 Hub 资源选项，请更新后端后刷新。');
        resourceOptions=options;
        for(const option of options){const input=field($('required-resources'),'resource_'+option.resource,option.label,'checkbox');input.dataset.resource=option.resource;input.checked=(selected?.requiredResources||[]).includes(option.resource);}
      }$('list').replaceChildren();
      $('list-status').textContent=agents.length?`共 ${agents.length} 个助手`:'暂无助手，请先在助手库新增。';
      for(const agent of agents){const button=document.createElement('button');button.type='button';button.dataset.agentId=agent.id;button.setAttribute('aria-pressed',String(selected?.id===agent.id));
        const title=document.createElement('strong');title.textContent=agent.name;const meta=document.createElement('small');meta.textContent=scope==='web'?(agent.web?.published?'网页已上架':'网页未上架'):agent.id;
        button.append(title,meta);button.onclick=()=>edit(agent);$('list').append(button);}
    } catch(error){showStatus($('list-status'),error.message);}
  }
  form.onsubmit=async event=>{
    event.preventDefault();if(uploading)return;const payload={};
    if(scope==='core'){
      if(resourceOptions===null){showStatus($('status'),'资源选项尚未加载，请刷新列表后重试。');return;}
      payload.requiredResources=Array.from(document.querySelectorAll('[data-resource]:checked'),input=>input.dataset.resource);
    }
    const value=name=>form.elements[name].value.trim();const lines=s=>s.split('\n').map(v=>v.trim()).filter(Boolean);
    if(scope==='core')coreFields.forEach(([key])=>payload[key]=key==='capabilities'?lines(value(key)):value(key));
    else {
      payload.stripePriceId=value('stripePriceId');
      payload.web={published:event.submitter?.id==='publish'?!selected.web?.published:!!selected.web?.published,inviteEnabled:form.elements.inviteEnabled.checked,subscriptionEnabled:form.elements.subscriptionEnabled.checked,sortOrder:Number(value('sortOrder')),content:{}};
      for(const locale of ['zh','en']){payload.web.content[locale]={};for(const [key] of copyFields)payload.web.content[locale][key]=key==='capabilities'?lines(value(locale+'_'+key)):value(locale+'_'+key);}
    }
    setBusy($('save'),true);if($('publish'))$('publish').disabled=true;
    try {const result=await adminRequest(endpoint+(selected?'/'+encodeURIComponent(selected.id):''),{method:selected?'PUT':'POST',body:JSON.stringify(payload)});edit(result);showStatus($('status'),'已保存。',true);await refresh();}
    catch(error){showStatus($('status'),error.message);}finally{setBusy($('save'),false);if($('publish'))$('publish').disabled=false;}
  };
  $('delete').onclick=async()=>{
    if(!selected||!confirm('删除此助手？已上架或已使用的助手不可删除。'))return;
    setBusy($('delete'),true);
    try {await adminRequest('/v1/admin/agent-catalog/'+encodeURIComponent(selected.id),{method:'DELETE'});selected=null;form.hidden=true;$('editor-title').textContent='选择助手';$('selection-hint').hidden=false;await refresh();}
    catch(error){showStatus($('status'),error.message);}finally{setBusy($('delete'),false);}
  };
  $('new').onclick=()=>scope==='core'?edit(null):location.assign('/admin/agents');$('refresh').onclick=refresh;refresh();
})();
