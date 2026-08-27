const $ = (id) => document.getElementById(id);
const enhancementStylesheet = document.createElement("link");
enhancementStylesheet.rel = "stylesheet";
enhancementStylesheet.href = "/admin/assets/admin-enhancements.css";
document.head.append(enhancementStylesheet);

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
  failed: "失败",
};

function pill(status) {
  return `<span class="pill ${esc(status)}">${esc(statusLabels[status] || status || "-")}</span>`;
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
