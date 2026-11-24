// SPDX-License-Identifier: GPL-3.0-only
"use strict";
const $ = id => document.getElementById(id);
const toggle = $("toggle");
const valueBox = $("limitValue");
const unitBox = $("limitUnit");
const applyButton = $("applyLimit");
const retryButton = $("retry");
let current = { desiredEnabled: false, consent: false };
let busy = false;
let dirty = false;
let requestId = 0;
let refreshing = false;

function formatSpeed(bytes) {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(2)} GB/s`;
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(2)} MB/s`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB/s`;
  return `${Math.round(bytes)} B/s`;
}

function updateToggle() {
  const needsConsent = !current.consent && !$(`consent`).checked;
  toggle.disabled = busy || needsConsent;
  toggle.classList.toggle("busy", busy);
  $("consentWarning").hidden = !needsConsent;
  toggle.title = needsConsent ? "Tick the consent checkbox below first" : "Toggle Chrome bandwidth limiting";
}

function render(status) {
  current = status;
  const connected = Boolean(status.desktopConnected && status.desktop);
  const routed = Boolean(status.proxyActive && connected);
  const active = Boolean(routed && status.desktop.limiting);
  if (!dirty) {
    valueBox.value = status.limitValue || "1";
    unitBox.value = status.limitUnit || "MB/s";
  }
  toggle.setAttribute("aria-checked", String(active));
  updateToggle();
  $("existingWarning").hidden = !routed;
  $("routingControl").hidden = !status.consent && !$('consent').checked;
  $("routingControl").disabled = busy;
  $("routingControl").textContent = routed ? "Disconnect proxy" : "Connect (unlimited)";
  const d = status.desktop;
  const vpnOn = routed ? Boolean(d.vpn_compat) : Boolean(status.vpnCompat);
  $("vpnStatus").textContent = routed ? (vpnOn ? "ON" : "OFF") : (vpnOn ? "OFF · saved ON" : "OFF · disconnected");
  $("vpnStatus").className = routed && vpnOn ? "on" : "";
  $("vpnNote").textContent = routed ? "Local proxy setting only; does not indicate VPN connection. Disconnect and reconnect to change." : "Choose the compatibility setting when connecting the proxy.";
  $("diagnostics").hidden = !routed;
  $("diagnostics").textContent = routed ? `Local connection diagnostics: Windows Chrome verification ${d.peer_verification ? "available" : "unavailable; credential fallback"}. Verified connections: ${d.verified_peers ?? 0}; auth challenges: ${d.auth_challenges ?? 0}. DNS failures: ${d.dns_failures ?? 0}; connection failures: ${d.connect_failures ?? 0}; blocked destinations: ${d.blocked_destinations ?? 0}. VPN compatibility: ${d.vpn_compat ? "ON" : "OFF"}; synthetic DNS answers: ${d.synthetic_seen ?? 0}; permitted: ${d.synthetic_allowed ?? 0}.${(d.dns_failures || d.connect_failures || d.blocked_destinations) ? " If a website fails while connected, use Disconnect proxy (not just cap OFF) and compare. Keep any required VPN active; report these counts." : ""}` : "";
  $("consentPanel").hidden = Boolean(status.consent);
  $("checkCompanion").disabled = busy;
  valueBox.disabled = busy;
  unitBox.disabled = busy;
  applyButton.disabled = busy || !dirty;
  $("capLabel").textContent = active ? "Current speed limit" : "Saved speed limit";
  $("currentCap").textContent = active ? status.desktop.cap_label : `${status.limitValue || '1'} ${status.limitUnit || 'MB/s'}`;
  $("liveSpeed").textContent = connected ? formatSpeed(status.desktop.live_bytes_per_second) : "—";
  $("connections").textContent = connected ? String(status.desktop.active_connections ?? 0) : "—";
  $("engineStatus").textContent = connected ? "Running" : "Stopped";
  $("engineStatus").className = `desktop-status ${connected ? 'online' : 'offline'}`;

  let label, kind, message;
  if (active) {
    label = "Active"; kind = "connected";
    message = "Limiter proxy is active";
  } else if (routed) {
    label = "Unlimited"; kind = "paused";
    message = "Cap OFF — local proxy stays connected";
  } else {
    label = status.error ? "Stopped" : "Disconnected"; kind = status.error ? "error" : "paused";
    message = status.error ? "Proxy stopped. See details below." : "Disconnected — prior proxy settings restored";
  }
  $("statusBadge").textContent = label;
  $("statusBadge").className = `badge ${kind}`;
  $("modeText").textContent = message;
  $("errorMessage").textContent = status.error || "";
  $("errorMessage").style.display = status.error ? "block" : "none";
  retryButton.hidden = !status.error || routed || !status.consent;
}

function send(action, data = {}) {
  return new Promise(resolve => {
    chrome.runtime.sendMessage({ action, ...data }, response => {
      const error = chrome.runtime.lastError;
      resolve(error ? { error: error.message } : response || { error:"No extension response" });
    });
  });
}

async function makeRequest(action, data = {}, changesConfig = false) {
  if (busy) return;
  busy = true;
  ++requestId;
  toggle.disabled = valueBox.disabled = unitBox.disabled = applyButton.disabled = true;
  updateToggle();
  $("routingControl").disabled = $("checkCompanion").disabled = retryButton.disabled = true;
  $("modeText").textContent = "Applying…";
  const response = await send(action, data);
  busy = false;
  $("routingControl").disabled = $("checkCompanion").disabled = retryButton.disabled = false;
  if (response && Object.hasOwn(response, "desiredEnabled")) {
    if (changesConfig && (!response.error || (response.limitValue === data.value && response.limitUnit === data.unit))) dirty = false;
    render(response);
  } else {
    $("errorMessage").textContent = response?.error || "Cannot contact extension";
    $("errorMessage").style.display = "block";
    valueBox.disabled = unitBox.disabled = false;
    updateToggle();
    applyButton.disabled = !dirty;
  }
}

async function refresh() {
  if (busy || refreshing) return;
  refreshing = true;
  const id = ++requestId;
  const response = await send("getStatus");
  refreshing = false;
  if (busy || id !== requestId) return;
  if (response && Object.hasOwn(response, "desiredEnabled")) render(response);
  else {
    $("errorMessage").textContent = response?.error || "Could not check status";
    $("errorMessage").style.display = "block";
  }
}

function markDirty() {
  dirty = true;
  applyButton.disabled = busy;
}
valueBox.addEventListener("input", markDirty);
unitBox.addEventListener("change", markDirty);
applyButton.addEventListener("click", () => makeRequest("setLimit", {value:valueBox.value.trim(), unit:unitBox.value}, true));
valueBox.addEventListener("keydown", event => {
  if (event.key === "Enter") applyButton.click();
});
$("consent").addEventListener("change", () => {updateToggle();$("routingControl").hidden = !current.consent && !$(`consent`).checked;});
$("checkCompanion").addEventListener("click", async () => { if (busy) return; busy=true; updateToggle(); $("checkCompanion").disabled=true; $("checkResult").textContent="Checking…"; const r=await send("checkCompanion"); busy=false; $("checkResult").textContent=r.companionReady||r.desktopConnected?"Windows companion is ready.":r.error||"Companion not found."; if(Object.hasOwn(r,"desiredEnabled"))render(r);else{updateToggle();$("checkCompanion").disabled=false;} });
let attachAction = null;
function connectAction(action, data={}) {
  if (current.proxyActive) { makeRequest(action,data); return; }
  attachAction = {action,data}; $("vpnCompat").checked = Boolean(current.vpnCompat); $("attachDialog").showModal();
}
$("cancelAttach").addEventListener("click",()=>{$("attachDialog").close();attachAction=null;});
$("confirmAttach").addEventListener("click",()=>{const a=attachAction;attachAction=null;$("attachDialog").close();if(a)makeRequest(a.action,{...a.data,vpnCompat:$("vpnCompat").checked});});
toggle.addEventListener("click", () => connectAction("setEnabled", {enabled: !(current.proxyActive && current.desktop?.limiting), consent: $("consent").checked}));
$("routingControl").addEventListener("click",()=>{if(current.proxyActive)makeRequest("disconnect");else connectAction("connect",{consent:$("consent").checked});});
retryButton.addEventListener("click", () => connectAction("retry"));
refresh();
setInterval(refresh, 2000);
