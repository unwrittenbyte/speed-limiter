// SPDX-License-Identifier: GPL-3.0-only
// Minimal DOM and Chrome status mock, exercising the actual popup rendering.
const fs=require('fs'),vm=require('vm'),assert=require('node:assert/strict'),path=require('path');
const html=fs.readFileSync(path.join(__dirname,'../extension/popup.html'),'utf8');
const els=new Map([...html.matchAll(/\bid="([^"]+)"/g)].map(([,id])=>[id,{textContent:'',className:'',style:{},classList:{toggle(){}},addEventListener(){},setAttribute(){},hidden:false,disabled:false,checked:false}]));
let state={desiredEnabled:false,consent:true,proxyActive:false,desktopConnected:false,vpnCompat:false};
const sandbox={document:{getElementById:id=>{assert(els.has(id),id);return els.get(id)}},chrome:{runtime:{sendMessage(m,cb){cb(state)}}},setInterval(){},console};
vm.createContext(sandbox);vm.runInContext(fs.readFileSync(path.join(__dirname,'../extension/popup.js'),'utf8'),sandbox);
function show(s){sandbox.s=s;vm.runInContext('render(s)',sandbox);return els.get('vpnStatus').textContent;}
assert.equal(show(state),'OFF · disconnected');
assert.equal(show({...state,vpnCompat:true}),'OFF · saved ON');
const connected={...state,desktopConnected:true,proxyActive:true,desktop:{vpn_compat:true,limiting:false}};
assert.equal(show(connected),'ON');
assert.equal(show({...connected,desktop:{vpn_compat:false,limiting:true},vpnCompat:true}),'OFF');
assert.equal(show({...connected,desktop:{vpn_compat:true,limiting:true}}),'ON');
assert.equal(els.get('vpnStatus').className,'on');
assert.match(els.get('vpnNote').textContent,/does not indicate VPN/);
console.log('PASS popup status: disconnected, saved ON, connected ON/OFF, actual engine status overrides saved preference.');
