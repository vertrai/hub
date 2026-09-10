const $ = (id) => document.getElementById(id);
if (document.querySelector(".shell > .side")) document.body.classList.add("admin-workspace");
if (!document.querySelector('link[href="/admin/assets/admin-enhancements.css"]')) {
  const enhancementStylesheet = document.createElement("link");
  enhancementStylesheet.rel = "stylesheet";
  enhancementStylesheet.href = "/admin/assets/admin-enhancements.css";
  document.head.append(enhancementStylesheet);
}

function adminHeaders() {
  return {
    "Content-Type": "application/json",
  };
}

async function adminRequest(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: { ...adminHeaders(), ...(options.headers || {}) },
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
	if (response.status === 401) {
	  window.location.assign("/admin/login");
	}
    const error = new Error(data.error || `HTTP ${response.status}`);
    error.status = response.status;
    error.data = data;
    throw error;
  }
  return data;
}

function initMobileNavigation() {
  const side = document.querySelector(".side");
  const brand = side?.querySelector(".brand");
  const nav = side?.querySelector(".nav");
  if (!side || !brand || !nav || brand.querySelector(".nav-toggle")) return;
  nav.id ||= "admin-navigation";
  const button = document.createElement("button");
  button.type = "button";
  button.className = "nav-toggle secondary";
  button.setAttribute("aria-controls", nav.id);
  button.setAttribute("aria-expanded", "false");
  button.innerHTML = '<span aria-hidden="true">☰</span><span>菜单</span>';
  button.addEventListener("click", () => {
    const open = side.classList.toggle("menu-open");
    button.setAttribute("aria-expanded", String(open));
  });
  brand.append(button);
}

function esc(value) {
  return String(value ?? "").replace(
    /[&<>'"]/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" })[
        c
      ],
  );
}

const statusLabels = {
  active: "正常",
  available: "可用",
  assigned: "已分配",
  authorized: "已授权",
  pending: "待处理",
  pending_2fa: "等待 2FA",
  spawning: "创建中",
  spawned: "已提交",
  running: "运行中",
  ready: "Xbox 已就绪",
  waiting_setup: "等待设置 Xbox",
  failed: "失败",
};

function pill(status) {
  return `<span class="pill ${esc(status)}">${esc(statusLabels[status] || status || "-")}</span>`;
}

function xboxSetupPill(status) {
  return pill(status === "ready" ? "ready" : "waiting_setup");
}

function setBusy(button, busy, text = "处理中…") {
  if (!button) return;
  if (!button.dataset.label) button.dataset.label = button.textContent;
  button.disabled = busy;
  button.textContent = busy ? text : button.dataset.label;
}

function showStatus(node, message, ok = false) {
  node.textContent = message;
  node.className = "status " + (ok ? "success" : "error");
}

document.addEventListener("DOMContentLoaded", () => {
  initMobileNavigation();
});


// Shared by LLM management and testing; never leave an unexplained empty select.
function initLLMUserSelector(select, status, onLoaded) {
  const retry = document.createElement("button");
  retry.type = "button";
  retry.className = "secondary";
  retry.textContent = "重新加载用户";
  select.parentElement.append(retry);
  async function load() {
    select.disabled = true;
    retry.disabled = true;
    select.replaceChildren(new Option("正在加载用户…", ""));
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 10000);
    let items;
    try {
      const data = await adminRequest("/v1/admin/llm/user-options", {signal: controller.signal, cache: "no-store"});
      items = data.items;
      if (!Array.isArray(items)) throw new Error("用户列表响应格式错误");
      select.replaceChildren(new Option(items.length ? "请选择用户" : "暂无用户，请先创建用户", ""),
        ...items.map(user => new Option(user.name || user.id, user.id)));
      select.disabled = !items.length;
      status.textContent = items.length ? "" : "暂无可用用户，请先到用户管理创建。";
    } catch (err) {
      select.replaceChildren(new Option("用户加载失败，请重试", ""));
      status.textContent = err.name === "AbortError" ? "用户列表加载超时，请点击重新加载用户。" : "用户列表加载失败：" + err.message;
    } finally {
      clearTimeout(timeout);
      retry.disabled = false;
    }
    if (items) {
      try { await onLoaded(items); } catch (err) { status.textContent = err.message; }
    }
  }
  retry.onclick = load;
  load();
}

function initResourceSearch() {
  document.querySelectorAll(".table-wrap").forEach(wrap => {
    const toolbar = document.createElement("div");
    toolbar.className = "resource-search";
    const input = document.createElement("input");
    input.type = "search";
    input.placeholder = "搜索名称、状态或 ID…";
    input.setAttribute("aria-label", "搜索此资源列表");
    const count = document.createElement("output");
    count.setAttribute("aria-live", "polite");
    toolbar.append(input, count);
    wrap.before(toolbar);
    const empty = document.createElement("div");
    empty.className = "table-no-matches";
    empty.textContent = "没有匹配的资源，请调整搜索条件。";
    empty.hidden = true;
    wrap.after(empty);
    function filter() {
      const rows = Array.from(wrap.querySelectorAll("tbody tr")).filter(row => !row.querySelector("td[colspan]"));
      const query = input.value.trim().toLocaleLowerCase();
      let visible = 0;
      rows.forEach(row => {
        row.hidden = query !== "" && !row.textContent.toLocaleLowerCase().includes(query);
        if (!row.hidden) visible++;
      });
      toolbar.hidden = !rows.length && !query;
      count.textContent = query ? visible + " / " + rows.length + " 条" : rows.length + " 条资源";
      empty.hidden = !rows.length || visible !== 0;
    }
    input.addEventListener("input", filter);
    new MutationObserver(filter).observe(wrap, {childList:true, subtree:true, characterData:true});
    filter();
  });
}
document.addEventListener("DOMContentLoaded", initResourceSearch);
