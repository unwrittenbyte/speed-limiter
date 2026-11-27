// SPDX-License-Identifier: GPL-3.0-only
"use strict";
const HOST = 'com.csl.speedlimiter_v1', ALARM = 'csl-watchdog';
// Companion protocol version, independent of the Chrome Store package version.
const VERSION = '1.0.6';
const UNITS = {'KB/s':1024,'MB/s':1048576,'GB/s':1073741824,'Mbps':125000};
let port = null, engine = null, auth = null, sequence = 0, pending = new Map(), error = null, queue = Promise.resolve();
let active = false, authAttempts = new Map();
function serial(fn){const t=queue.then(fn);queue=t.catch(()=>{});return t;}
function check(value,unit){const n=Number(value)*UNITS[unit];if(typeof value!=='string'||value.length>24||!/^(?:\d+(?:\.\d*)?|\.\d+)$/.test(value)||!Object.hasOwn(UNITS,unit)||!Number.isFinite(n)||n<1024||n>1073741824)throw Error('Allowed range: 1 KB/s to 1 GB/s.');}
async function config(){let c=await chrome.storage.local.get({desiredEnabled:false,routingWanted:false,vpnCompat:false,limitValue:'1',limitUnit:'MB/s',consent:false});try{check(c.limitValue,c.limitUnit);}catch{c.limitValue='1';c.limitUnit='MB/s';}return c;}
function request(command, fields={}){if(!port)return Promise.reject(Error('Companion not connected.'));let id=++sequence;return new Promise((resolve,reject)=>{let timer=setTimeout(()=>{pending.delete(id);reject(Error('Companion did not respond within 25 seconds. If antivirus is scanning it, let the scan finish and retry.'));},25000);pending.set(id,{resolve,reject,timer});try{port.postMessage({id,command,...fields});}catch(e){clearTimeout(timer);pending.delete(id);reject(e);}});}
function closePort(){let p=port;port=null;engine=null;auth=null;active=false;authAttempts.clear();if(p)try{p.disconnect();}catch{};for(let [,v] of pending){clearTimeout(v.timer);v.reject(Error('Companion disconnected.'));}pending.clear();}
function friendly(msg){if(/native messaging host not found|native messaging host was not found|host is not registered/i.test(msg))return "Windows companion is not installed. Install the matching Windows companion, then retry.";return String(msg);}
function openPort(){let p=chrome.runtime.connectNative(HOST);port=p;p.onMessage.addListener(m=>{if(port!==p||!m)return;let v=pending.get(m.id);if(!v)return;pending.delete(m.id);clearTimeout(v.timer);m.ok?v.resolve(m):v.reject(Error(m.error||'Companion error.'));});p.onDisconnect.addListener(()=>{const msg=chrome.runtime.lastError?.message||'Companion disconnected.';if(port!==p)return;closePort();serial(async()=>{if(!port)await fail(msg);}).catch(()=>{});});}
// A read-only handshake, isolated from the routing port and proxy state.
function probeFailure(message) {
 const detail=String(message||'Companion did not respond.');
 if(/native messaging host not found|native messaging host was not found|host is not registered/i.test(detail))return {companionStatus:'missing',companionDetail:detail};
 if(/access to the specified native messaging host is forbidden|native messaging.*(?:forbidden|not allowed|access denied)/i.test(detail))return {companionStatus:'forbidden',companionDetail:detail};
 return {companionStatus:'unavailable',companionDetail:detail};
}
async function probeCompanion(){
 if(active&&engine?.version===VERSION)return {companionStatus:'ready'};
 if((await chrome.runtime.getPlatformInfo()).os!=='win')return {companionStatus:'unsupported'};
 return new Promise(resolve=>{
  let probe=null,finished=false,timer=null;
  function finish(result){if(finished)return;finished=true;clearTimeout(timer);if(probe)try{probe.disconnect();}catch{}resolve(result);}
  try{
   probe=chrome.runtime.connectNative(HOST);
   probe.onMessage.addListener(m=>{
    if(!m||m.id!==1)return;
    if(!m.ok)return finish(probeFailure(m.error));
    finish(m.state?.version===VERSION?{companionStatus:'ready'}:{companionStatus:'mismatch'});
   });
   probe.onDisconnect.addListener(()=>{const msg=chrome.runtime.lastError?.message;finish(probeFailure(msg));});
   timer=setTimeout(()=>finish(probeFailure('The Windows companion did not respond within 25 seconds.')),25000);
   probe.postMessage({id:1,command:'hello'});
  }catch(e){finish(probeFailure(e.message||e));}
 });
}
async function clearProxy(){active=false;await chrome.proxy.settings.clear({scope:'regular'});}
async function fail(msg){error=friendly(msg);try{await clearProxy();}catch(e){error+=' Proxy cleanup failed: '+e.message;}closePort();try{await chrome.storage.local.set({routingWanted:false});}catch{};await badge();}
function pac(p){return {mode:'pac_script',pacScript:{mandatory:false,data:`function FindProxyForURL(url, host) {\n if (isPlainHostName(host) || dnsDomainIs(host, '.local') || dnsDomainIs(host, '.localhost') || host === 'localhost' || /^(127|10)\\.[0-9]+\\.[0-9]+\\.[0-9]+$/.test(host) || /^(192\\.168|169\\.254|172\\.(1[6-9]|2[0-9]|3[01]))\\.[0-9]+\\.[0-9]+$/.test(host) || host === '::1' || host === '[::1]' || /^\\[?(fc|fd|fe[89ab])/i.test(host) && host.indexOf(':') >= 0) return 'DIRECT';\n return 'PROXY 127.0.0.1:${p}; DIRECT';\n}`}};}
async function proxyInfo(){return chrome.proxy.settings.get({incognito:false});}
async function start(c){
 const info=await proxyInfo();if(!['controllable_by_this_extension','controlled_by_this_extension'].includes(info.levelOfControl))throw Error('Another extension or administrator controls Chrome proxy settings.');
 if(info.levelOfControl==='controlled_by_this_extension')await clearProxy();
 const before=await proxyInfo();if(!['direct','system'].includes(before.value?.mode))throw Error('An existing proxy configuration is active. Disable it before using the limiter.');
 openPort();let m=await request('start',{value:c.limitValue,unit:c.limitUnit,enabled:c.desiredEnabled,vpnCompat:c.vpnCompat===true});
 if(!m.state||m.state.version!==VERSION||!Number.isInteger(m.state.port)||m.state.port<1024||m.state.port>65535||m.auth?.username!=='csl'||!/^[A-Za-z0-9_-]{43}$/.test(m.auth?.password||''))throw Error('Companion version mismatch. Install the matching Windows companion.');
 engine=m.state;auth={port:engine.port,username:m.auth.username,password:m.auth.password};
 await chrome.proxy.settings.set({value:pac(engine.port),scope:'regular'});let after=await proxyInfo();if(after.levelOfControl!=='controlled_by_this_extension'||after.value?.pacScript?.data!==pac(engine.port).pacScript.data)throw Error('Chrome did not apply the limiter proxy.');active=true;error=null;
}
async function ensure(c){if(!port){await start(c);return;}let info=await proxyInfo();if(info.levelOfControl!=='controlled_by_this_extension'||info.value?.pacScript?.data!==pac(engine.port).pacScript.data)throw Error('Chrome proxy settings changed. Limiter stopped.');engine=(await request('status')).state;if(!engine?.engine_running)throw Error('Companion engine stopped.');if(engine.limiting!==c.desiredEnabled)engine=(await request('setEnabled',{enabled:c.desiredEnabled})).state;active=true;}
async function stop(){let cleanupError=null;try{await clearProxy();}catch(e){cleanupError=e;}if(port)try{await request('stop');}catch{};closePort();if(cleanupError)throw cleanupError;}
async function badge(){try{await chrome.action.setBadgeText({text:active?(engine?.limiting?'ON':'∞'):error?'!':''});await chrome.action.setBadgeBackgroundColor({color:active?'#19734d':'#a33b43'});}catch{}}
async function snapshot(){let c=await config();return {error,desiredEnabled:c.desiredEnabled,routingWanted:c.routingWanted,vpnCompat:c.vpnCompat===true,proxyActive:active,desktopConnected:!!engine,desktop:engine,limitValue:c.limitValue,limitUnit:c.limitUnit,consent:c.consent};}
async function sync(){let c=await config();try{if(c.routingWanted&&c.consent)await ensure(c);else if(port||active||(await proxyInfo()).levelOfControl==='controlled_by_this_extension')await stop();}catch(e){await fail(e.message||e);}await badge();return snapshot();}
// Credentials are returned ONLY to our matching loopback proxy challenge, never to websites.
chrome.webRequest.onAuthRequired.addListener((d,cb)=>{let a=auth;if(!d.isProxy||d.challenger?.host!=='127.0.0.1'||d.realm!=='Chrome Speed Limiter'||String(d.scheme).toLowerCase()!=='basic'){cb({});return;}if(!a||d.challenger.port!==a.port){cb({cancel:true});return;}if(authAttempts.has(d.requestId)){cb({cancel:true});return;}if(authAttempts.size>=4096)authAttempts.clear();authAttempts.set(d.requestId,true);cb({authCredentials:{username:a.username,password:a.password}});},{urls:['<all_urls>']},['asyncBlocking']);
for(let event of [chrome.webRequest.onCompleted,chrome.webRequest.onErrorOccurred])event.addListener(d=>authAttempts.delete(d.requestId),{urls:['<all_urls>']});
chrome.runtime.onMessage.addListener((m,s,respond)=>{if(s.id!==chrome.runtime.id||s.url!==chrome.runtime.getURL('popup.html')||!m||!['getStatus','setEnabled','setLimit','retry','checkCompanion','disconnect','connect'].includes(m.action))return false;serial(async()=>{
 try{if(['setEnabled','retry','connect'].includes(m.action)){let c=await config();let enable=m.action==='retry'?c.desiredEnabled:m.action==='connect'?false:m.enabled===true;if(!c.consent){if(m.consent!==true)throw Error('Review the local traffic disclosure and confirm before connecting.');await chrome.storage.local.set({consent:true});}error=null;await chrome.storage.local.set({desiredEnabled:enable,routingWanted:true,...(typeof m.vpnCompat==='boolean'&&!active?{vpnCompat:m.vpnCompat}:{})});}
 else if(m.action==='disconnect'){error=null;await chrome.storage.local.set({desiredEnabled:false,routingWanted:false});}
 else if(m.action==='setLimit'){let value=String(m.value??'').trim(),unit=String(m.unit??'');check(value,unit);if(port)engine=(await request('setLimit',{value,unit})).state;await chrome.storage.local.set({limitValue:value,limitUnit:unit});error=null;}
 else if(m.action==='checkCompanion'){let result;try{result=await probeCompanion();}catch(e){result=probeFailure(e.message||e);}return {...await snapshot(),...result,companionReady:result.companionStatus==='ready'};}
 return await sync();}catch(e){if(m.action==='setLimit'){error=e.message; if(port && active){try{await ensure(await config());}catch(x){await fail(x.message);}} return snapshot();}await fail(e.message||e);return snapshot();}
 }).then(respond,async e=>respond({error:String(e)}));return true;});
chrome.proxy.settings.onChange.addListener(()=>{if(active)serial(sync).catch(()=>{});});
chrome.alarms.onAlarm.addListener(a=>{if(a.name===ALARM)serial(sync).catch(()=>{});});
chrome.runtime.onInstalled.addListener(details=>{
 // Preserve the existing safety reset on installation/update. Only new installs get a welcome tab.
 serial(async()=>{await chrome.storage.local.set({desiredEnabled:false,routingWanted:false,consent:false,vpnCompat:false});await sync();}).catch(()=>{});
 if(details.reason==='install')chrome.tabs.create({url:chrome.runtime.getURL('setup.html')}).catch(()=>{});
});
chrome.runtime.onStartup.addListener(()=>serial(sync).catch(()=>{}));
chrome.alarms.create(ALARM,{periodInMinutes:0.5});serial(sync).catch(()=>{});
