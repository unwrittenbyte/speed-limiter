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
let checkingCompanion = false;
let companionStatus = "checking";
let companionDetail = "";
function companionReady() { return current.desktopConnected || companionStatus === "ready"; }
function renderSetup() {
  const ready = companionReady();
  const state = checkingCompanion ? "checking" : companionStatus;
  $("setupCard").hidden = ready;
  $("limiterControls").hidden = !ready;
  $("checkCompanion").hidden = !ready;
  $("setupCheck").disabled = busy || checkingCompanion;
  $("setupCheck").textContent = checkingCompanion || state === "checking" ? "Checking installation…" : "Check installation";
  $("setupDownload").hidden = !["missing", "mismatch", "unavailable"].includes(state);
  $("setupSteps").hidden = $("setupDownload").hidden;
  $("setupDetail").hidden = !["unavailable", "forbidden"].includes(state) || !companionDetail;
  $("setupDetail").textContent = companionDetail;
  const copy = {
    checking: ["Checking Windows companion…", "Speed Limiter requires a free Windows companion app. Checking your installation."],
    missing: ["One last step!", "Speed Limiter needs a free Windows companion app to limit your download speed. Download and install it to get started."],
    mismatch: ["Companion update required", "Your installed companion is not compatible with this extension. Download the matching v1.0.6 Windows companion."],
    unavailable: ["Can't connect to the companion", "The free Windows companion could not be verified. If it is already installed, try checking again or use installation help."],
    forbidden: ["Chrome blocked the companion connection", "The companion may already be installed, but Chrome did not authorize this extension to connect. Make sure you are using the official extension, then follow installation help if the problem persists."],
    unsupported: ["Windows required", "Speed Limiter requires the Windows companion app. This operating system is not supported."],
    ready: ["Windows companion ready", "Your Windows companion is ready to use."]
  }[state] || ["Check your installation", "Speed Limiter requires a free Windows companion app."];
  $("setupTitle").textContent = copy[0];
  $("setupText").textContent = copy[1];
}


function formatSpeed(bytes) {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(2)} GB/s`;
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(2)} MB/s`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB/s`;
  return `${Math.round(bytes)} B/s`;
}

function updateToggle() {
  const needsConsent = !current.consent && !$(`consent`).checked;
  toggle.disabled = busy || checkingCompanion || !companionReady() || needsConsent;
  toggle.classList.toggle("busy", busy);
  $("consentWarning").hidden = !needsConsent || !companionReady();
  toggle.title = !companionReady() ? "Set up the Windows companion first" : needsConsent ? "Tick the consent checkbox below first" : "Toggle Chrome bandwidth limiting";
}

function render(status) {
  if (companionStatus === "ready" && /Windows companion is not installed|Companion version mismatch/i.test(status.error || "")) status = {...status,error:null};
  current = status;
  if (status.desktopConnected) { companionStatus = "ready"; companionDetail = ""; }

  renderSetup();
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
  $("routingControl").hidden = !companionReady() || (!status.consent && !$('consent').checked);
  $("routingControl").disabled = busy || checkingCompanion;
  $("routingControl").textContent = routed ? "Disconnect proxy" : "Connect (unlimited)";
  const d = status.desktop;
  const vpnOn = routed ? Boolean(d.vpn_compat) : Boolean(status.vpnCompat);
  $("vpnStatus").textContent = routed ? (vpnOn ? "ON" : "OFF") : (vpnOn ? "OFF · saved ON" : "OFF · disconnected");
  $("vpnStatus").className = routed && vpnOn ? "on" : "";
  $("vpnNote").textContent = routed ? "Local proxy setting only; does not indicate VPN connection. Disconnect and reconnect to change." : "Choose the compatibility setting when connecting the proxy.";
  $("diagnostics").hidden = !routed;
  $("diagnostics").textContent = routed ? `Local connection diagnostics: Windows Chrome verification ${d.peer_verification ? "available" : "unavailable; credential fallback"}. Verified connections: ${d.verified_peers ?? 0}; auth challenges: ${d.auth_challenges ?? 0}. DNS failures: ${d.dns_failures ?? 0}; connection failures: ${d.connect_failures ?? 0}; blocked destinations: ${d.blocked_destinations ?? 0}. VPN compatibility: ${d.vpn_compat ? "ON" : "OFF"}; synthetic DNS answers: ${d.synthetic_seen ?? 0}; permitted: ${d.synthetic_allowed ?? 0}.${(d.dns_failures || d.connect_failures || d.blocked_destinations) ? " If a website fails while connected, use Disconnect proxy (not just cap OFF) and compare. Keep any required VPN active; report these counts." : ""}` : "";
  $("consentPanel").hidden = Boolean(status.consent) || !companionReady();
  $("checkCompanion").disabled = busy || checkingCompanion;
  valueBox.disabled = busy || checkingCompanion;
  unitBox.disabled = busy || checkingCompanion;
  applyButton.disabled = busy || checkingCompanion || !dirty;
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
    message = !companionReady() ? "Complete companion setup first" : status.error ? "Proxy stopped. See details below." : "Disconnected — prior proxy settings restored";
    if (!companionReady()) { label = checkingCompanion || companionStatus === "checking" ? "Checking" : companionStatus === "unsupported" ? "Windows required" : "Setup required"; kind = "offline"; }
  }
  $("statusBadge").textContent = label;
  $("statusBadge").className = `badge ${kind}`;
  $("modeText").textContent = message;
  $("errorMessage").textContent = status.error || "";
  $("errorMessage").style.display = status.error ? "block" : "none";
  retryButton.hidden = !status.error || routed || !status.consent || !companionReady();
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
  if (busy || checkingCompanion) return;
  busy = true;
  ++requestId;
  toggle.disabled = valueBox.disabled = unitBox.disabled = applyButton.disabled = true;
  updateToggle();
  renderSetup();
  $("routingControl").disabled = $("checkCompanion").disabled = retryButton.disabled = true;
  $("modeText").textContent = "Applying…";
  const response = await send(action, data);
  busy = false;
  $("routingControl").disabled = $("checkCompanion").disabled = retryButton.disabled = false;
  if (response && Object.hasOwn(response, "desiredEnabled")) {
    if (changesConfig && (!response.error || (response.limitValue === data.value && response.limitUnit === data.unit))) dirty = false;
    if (/Windows companion is not installed/i.test(response.error || "")) companionStatus = "missing";
    else if (/Companion version mismatch/i.test(response.error || "")) companionStatus = "mismatch";
    render(response);
  } else {
    $("errorMessage").textContent = response?.error || "Cannot contact extension";
    $("errorMessage").style.display = "block";
    valueBox.disabled = unitBox.disabled = false;
    updateToggle();
    applyButton.disabled = !dirty;
    renderSetup();
  }
}

async function refresh() {
  if (busy || refreshing || checkingCompanion) return;
  refreshing = true;
  const id = ++requestId;
  const response = await send("getStatus");
  refreshing = false;
  if (busy || checkingCompanion || id !== requestId) return;
  if (response && Object.hasOwn(response, "desiredEnabled")) render(response);
  else {
    $("errorMessage").textContent = response?.error || "Could not check status";
    $("errorMessage").style.display = "block";
  }
}

function markDirty() {
  dirty = true;
  applyButton.disabled = busy || checkingCompanion;
}
valueBox.addEventListener("input", markDirty);
unitBox.addEventListener("change", markDirty);
applyButton.addEventListener("click", () => makeRequest("setLimit", {value:valueBox.value.trim(), unit:unitBox.value}, true));
valueBox.addEventListener("keydown", event => {
  if (event.key === "Enter") applyButton.click();
});
$("consent").addEventListener("change", () => {updateToggle();$("routingControl").hidden = !companionReady() || (!current.consent && !$(`consent`).checked);});
async function checkInstallation() {
  if (busy || checkingCompanion) return;
  checkingCompanion = true;
  ++requestId;
  companionStatus = "checking";
  companionDetail = "";
  $("checkResult").textContent = "";
  render(current);
  const r = await send("checkCompanion");
  checkingCompanion = false;
  companionStatus = r.companionStatus || (r.desktopConnected ? "ready" : "unavailable");
  companionDetail = r.companionDetail || (!r.companionStatus ? r.error || "Could not check installation." : "");
  if (Object.hasOwn(r,"desiredEnabled")) {
    // Old routing errors must not override a fresh, successful installation check.
    if (companionStatus === "ready" && /Windows companion is not installed|Companion version mismatch/i.test(r.error || "")) r.error = null;
    current = r;
  }
  render(current);
  $("checkResult").textContent = companionReady() ? "Windows companion is ready." : "";
}
$("checkCompanion").addEventListener("click", checkInstallation);
$("setupCheck").addEventListener("click", checkInstallation);
let attachAction = null;
function connectAction(action, data={}) {
  if (busy || checkingCompanion || !companionReady()) return;
  if (current.proxyActive) { makeRequest(action,data); return; }
  attachAction = {action,data}; $("vpnCompat").checked = Boolean(current.vpnCompat); $("attachDialog").showModal();
}
$("cancelAttach").addEventListener("click",()=>{$("attachDialog").close();attachAction=null;});
$("confirmAttach").addEventListener("click",()=>{const a=attachAction;attachAction=null;$("attachDialog").close();if(a)makeRequest(a.action,{...a.data,vpnCompat:$("vpnCompat").checked});});
toggle.addEventListener("click", () => connectAction("setEnabled", {enabled: !(current.proxyActive && current.desktop?.limiting), consent: $("consent").checked}));
$("routingControl").addEventListener("click",()=>{if(current.proxyActive)makeRequest("disconnect");else connectAction("connect",{consent:$("consent").checked});});
retryButton.addEventListener("click", () => connectAction("retry"));
async function initialize(){
  await refresh();
  if (!current.desktopConnected) await checkInstallation();
}
initialize();
setInterval(refresh, 2000);
