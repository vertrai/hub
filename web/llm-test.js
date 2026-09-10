let users=[],activeSecret='',generation=0,requestController=null;
const el=id=>document.getElementById(id);
const testAttachments=[];
let imageReadQueue=Promise.resolve(),pendingImageReads=0;
function renderTestImages(){
 el('imagePreviews').replaceChildren(...testAttachments.map(attachment=>{
  const figure=document.createElement('figure');figure.className='image-preview';
  const img=document.createElement('img');img.src=attachment.url;img.alt=attachment.name;
  const caption=document.createElement('figcaption');caption.textContent=attachment.name;caption.title=attachment.name;
  const remove=document.createElement('button');remove.type='button';remove.className='secondary';remove.textContent='×';remove.setAttribute('aria-label','移除图片 '+attachment.name);remove.title='移除图片';
  remove.onclick=()=>{testAttachments.splice(testAttachments.indexOf(attachment),1);renderTestImages();};
  figure.append(img,caption,remove);return figure;
 }));
}
function addTestImages(files){
 const batch=Array.from(files);pendingImageReads++;
 imageReadQueue=imageReadQueue.then(async()=>{
  el('imageStatus').textContent='';
  for(const file of batch){
   if(!['image/png','image/jpeg','image/webp','image/gif'].includes(file.type)){el('imageStatus').textContent='请选择 PNG、JPEG、WebP 或 GIF 图片。';continue;}
   if(testAttachments.length>=4 || testAttachments.reduce((sum,a)=>sum+a.size,0)+file.size>4*1024*1024){el('imageStatus').textContent='最多添加 4 张图片，总大小不能超过 4 MB。';continue;}
   try{
    const url=await new Promise((resolve,reject)=>{const reader=new FileReader();reader.onload=()=>resolve(reader.result);reader.onerror=()=>reject(new Error('图片读取失败，请重新添加。'));reader.readAsDataURL(file);});
    testAttachments.push({name:file.name || '粘贴的图片',size:file.size,url});renderTestImages();
   }catch(err){el('imageStatus').textContent=err.message;}
  }
 }).finally(()=>{pendingImageReads--;});
 return imageReadQueue;
}
el('addTestImages').onclick=()=>el('testImages').click();
el('testImages').onchange=event=>{addTestImages(event.target.files);event.target.value='';};
el('testPrompt').onpaste=event=>{
 const files=Array.from(event.clipboardData?.items || []).filter(item=>item.kind==='file' && item.type.startsWith('image/')).map(item=>item.getAsFile()).filter(Boolean);
 if(files.length){event.preventDefault();addTestImages(files);}
};
el('testComposer').ondragover=event=>{event.preventDefault();el('testComposer').classList.add('dragging');};
el('testComposer').ondragleave=()=>el('testComposer').classList.remove('dragging');
el('testComposer').ondrop=event=>{event.preventDefault();el('testComposer').classList.remove('dragging');addTestImages(event.dataTransfer.files);};
function testMessageContent(){
 const text=el('testPrompt').value.trim();
 if(!testAttachments.length)return text;
 return [...(text?[{type:'text',text}]:[]),...testAttachments.map(a=>({type:'image_url',image_url:{url:a.url}}))];
}
function resetTest(){generation++;activeSecret='';el('resolvedKey').value='';el('createKeyLink').hidden=true;if(requestController)requestController.abort();el('testModel').replaceChildren();el('testAliasChoices').replaceChildren();el('testDirectChoices').replaceChildren();el('sendTest').disabled=true;el('routeInfo').textContent='';el('answer').textContent='等待测试';el('rawResponse').textContent='';el('testStatus').textContent='';return generation;}
async function loadModels(secret,version,defaultModel){
 const response=await fetch('/llm/v1/models',{headers:{Authorization:'Bearer '+secret},credentials:'omit',cache:'no-store'});
 const data=await response.json();if(version!==generation)return;
 if(!response.ok)throw new Error(data.error?.message || '模型加载失败 · HTTP '+response.status);
 const models=data.data || [];el('testModel').replaceChildren(...models.map(m=>{const option=document.createElement('option');option.value=m.id;option.textContent=m.id==='hub-chat'?'hub-chat（默认模型）'+(defaultModel?' → '+defaultModel:''):m.id;return option;}));
 if(models.some(m=>m.id==='hub-chat'))el('testModel').value='hub-chat';
 for(const m of models){
 const label=document.createElement('label');label.className='test-model-option';
 const radio=document.createElement('input');radio.type='radio';radio.name='testModelChoice';radio.value=m.id;radio.checked=el('testModel').value===m.id;
 radio.onchange=()=>{el('testModel').value=m.id;};
 const text=document.createElement('span');text.textContent=m.id==='hub-chat'?'hub-chat → '+(defaultModel || '此 Key 的默认模型'):m.id;
 label.append(radio,text);el(m.id==='hub-chat'?'testAliasChoices':'testDirectChoices').append(label);
 }
 if(!models.some(m=>m.id==='hub-chat'))el('testAliasChoices').textContent='默认路由尚未配置或不可用。';
 if(!models.some(m=>m.id!=='hub-chat'))el('testDirectChoices').textContent='此 Key 暂无可用的对外模型，请检查授权策略。';
 activeSecret=secret;el('sendTest').disabled=models.length===0;el('identityStatus').textContent=models.length?'已加载此 Key 的授权模型。':'此 Key 暂无可用模型。';
}
el('testUser').onchange=()=>{const user=users.find(u=>u.id===el('testUser').value);el('testHubKey').replaceChildren(...(user?.accessKeys || []).filter(k=>k.status==='active').map(k=>{const option=document.createElement('option');option.value=k.id;option.textContent=k.name || k.id;return option;}));return el('testHubKey').onchange();};
el('testHubKey').onchange=async()=>{const version=resetTest();const id=el('testHubKey').value;if(!id){el('identityStatus').textContent='请选择有 Hub Key 的用户。';return;}el('identityStatus').textContent='正在读取已有 LLM Key…';try{
 const data=await adminRequest('/v1/admin/access-keys/'+encodeURIComponent(id)+'/llm-resource?userId='+encodeURIComponent(el('testUser').value));if(version!==generation)return;
 if(!data.exists){el('createKeyLink').hidden=false;throw new Error('此 Hub Key 尚未创建 LLM Key，请先创建。');}if(data.revoked)throw new Error('LLM Key 已撤销。');
 if(!data.apiKey)throw new Error('此 LLM Key 的凭据不可用，请检查中转站配置。');
 el('resolvedKey').value=data.apiKey;el('routeInfo').textContent='hub-chat（默认模型）→ '+data.defaultModel;await loadModels(data.apiKey,version,data.defaultModel);
 }catch(err){if(version===generation)el('identityStatus').textContent=err.message;}};
el('reloadKey').onclick=()=>el('testHubKey').onchange();
el('cancelTest').onclick=()=>requestController?.abort();
el('testForm').onsubmit=async event=>{event.preventDefault();if(!activeSecret || requestController)return;
 if(pendingImageReads){el('testStatus').textContent='图片正在读取，请稍后开始测试。';return;}
 const content=testMessageContent();
 if(!content.length){el('testStatus').textContent='请输入测试消息或添加图片。';return;}
 const payload=JSON.stringify({model:el('testModel').value,messages:[{role:'user',content}],stream:false});
 if(new Blob([payload]).size>8*1024*1024){el('testStatus').textContent='测试内容超过 8 MB，请减少图片或文字。';return;}
 const version=generation,controller=new AbortController();requestController=controller;el('sendTest').disabled=true;el('cancelTest').disabled=false;el('testStatus').textContent='请求中…';el('answer').textContent='';el('rawResponse').textContent='';const start=performance.now();const timeout=setTimeout(()=>controller.abort(),120000);
 try{const response=await fetch('/llm/v1/chat/completions',{method:'POST',credentials:'omit',headers:{'Content-Type':'application/json',Authorization:'Bearer '+activeSecret},body:payload,signal:controller.signal});const raw=await response.text();if(version!==generation)return;let data;try{data=JSON.parse(raw);}catch{}el('rawResponse').textContent=data?JSON.stringify(data,null,2):raw;el('testStatus').textContent=`${response.ok?'成功':'失败'} · HTTP ${response.status} · ${((performance.now()-start)/1000).toFixed(2)} 秒`;el('answer').textContent=response.ok?(data?.choices?.[0]?.message?.content || '响应无文本内容，请查看原始响应。'):(data?.error?.message || '请求失败，请查看原始响应。');
 }catch(err){if(version===generation)el('testStatus').textContent=err.name==='AbortError'?'请求已停止或超时。':err.message;}finally{clearTimeout(timeout);requestController=null;el('cancelTest').disabled=true;el('sendTest').disabled=!activeSecret;}};
initLLMUserSelector(el('testUser'), el('identityStatus'), async items => {
 users=items; resetTest(); el('testHubKey').replaceChildren();
 const first=users.find(u=>(u.accessKeys || []).some(k=>k.status==='active'));
 if(first){el('testUser').value=first.id;await el('testUser').onchange();}
 else if(users.length){el('identityStatus').textContent='暂无可用 Hub Key，请先到用户管理创建。';}
});
